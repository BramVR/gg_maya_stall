package cli

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mayaHostCapabilityRecordVersion = 1
const mayaHostCapabilityFreshness = time.Minute

// Small forward skew keeps healthy Agents eligible without allowing them to extend freshness materially.
const mayaHostCapabilityClockSkew = 30 * time.Second

type versionRequirement struct {
	Exact   string `yaml:"exact" json:"exact,omitempty"`
	Minimum string `yaml:"minimum" json:"minimum,omitempty"`
}

type sessionBrokerRequirement struct {
	Exact    string   `yaml:"exact" json:"exact,omitempty"`
	Minimum  string   `yaml:"minimum" json:"minimum,omitempty"`
	Features []string `yaml:"features" json:"features,omitempty"`
}

type scenarioRequirements struct {
	Maya                   versionRequirement       `yaml:"maya" json:"maya,omitempty"`
	Python                 versionRequirement       `yaml:"python" json:"python,omitempty"`
	SessionBroker          sessionBrokerRequirement `yaml:"sessionBroker" json:"sessionBroker,omitempty"`
	Capture                []string                 `yaml:"capture" json:"capture,omitempty"`
	Control                []string                 `yaml:"control" json:"control,omitempty"`
	Renderers              []string                 `yaml:"renderers" json:"renderers,omitempty"`
	GPU                    []string                 `yaml:"gpu" json:"gpu,omitempty"`
	Display                []string                 `yaml:"display" json:"display,omitempty"`
	Licensing              []string                 `yaml:"licensing" json:"licensing,omitempty"`
	TrustedPluginArtifacts *bool                    `yaml:"trustedPluginArtifacts" json:"trustedPluginArtifacts,omitempty"`
}

type sessionBrokerCapabilities struct {
	Version  string   `yaml:"version" json:"version"`
	Features []string `yaml:"features" json:"features"`
}

type mayaHostCapabilities struct {
	MayaBuilds             []string                  `yaml:"mayaBuilds" json:"mayaBuilds"`
	SessionMayaBuild       string                    `yaml:"sessionMayaBuild" json:"sessionMayaBuild"`
	Python                 string                    `yaml:"python" json:"python"`
	SessionBroker          sessionBrokerCapabilities `yaml:"sessionBroker" json:"sessionBroker"`
	Capture                []string                  `yaml:"capture" json:"capture"`
	Control                []string                  `yaml:"control" json:"control"`
	Renderers              []string                  `yaml:"renderers" json:"renderers"`
	GPU                    []string                  `yaml:"gpu" json:"gpu"`
	Display                []string                  `yaml:"display" json:"display"`
	Licensing              []string                  `yaml:"licensing" json:"licensing"`
	TrustedPluginArtifacts *bool                     `yaml:"trustedPluginArtifacts" json:"trustedPluginArtifacts"`
}

type mayaHostCapabilityRecord struct {
	Version        int                  `json:"version"`
	ReportedAt     string               `json:"reportedAt"`
	Online         bool                 `json:"online"`
	Health         string               `json:"health"`
	Maintenance    bool                 `json:"maintenance"`
	Quarantined    bool                 `json:"quarantined"`
	TargetProfiles []string             `json:"targetProfiles"`
	Capabilities   mayaHostCapabilities `json:"capabilities"`
}

type hostCompatibilityDecision struct {
	Compatible        bool
	Reasons           []string
	SelectedMayaBuild string
}

func normalizedScenarioRequirements(scenario scenarioConfig) scenarioRequirements {
	requirements := scenario.Requirements
	requirements.SessionBroker.Features = appendUniqueCapability(requirements.SessionBroker.Features, "script.execute")
	if scenario.Evidence.Screenshots.Enabled {
		requirements.Capture = appendUniqueCapability(requirements.Capture, "screenshot")
	}
	if scenario.Evidence.Recording.Enabled {
		requirements.Capture = appendUniqueCapability(requirements.Capture, "recording")
	}
	for _, validator := range scenario.Validators {
		if validator.Type == "visualEvidence" && (validator.Required == nil || *validator.Required) {
			requirements.Capture = appendUniqueCapability(requirements.Capture, "visual-evidence")
			break
		}
	}
	if strings.TrimSpace(scenario.SelectedMayaBuild) != "" {
		requirements.Maya = versionRequirement{Exact: strings.TrimSpace(scenario.SelectedMayaBuild)}
		return requirements
	}
	if requirements.Maya.Exact == "" && requirements.Maya.Minimum == "" && strings.TrimSpace(scenario.MayaVersion) != "" {
		requirements.Maya.Exact = strings.TrimSpace(scenario.MayaVersion)
	}
	return requirements
}

func appendUniqueCapability(values []string, required string) []string {
	result := append([]string(nil), values...)
	if !containsCapability(result, required) {
		result = append(result, required)
	}
	return result
}

func validateScenarioRequirements(scenario scenarioConfig) error {
	if strings.TrimSpace(scenario.MayaVersion) != "" && (strings.TrimSpace(scenario.Requirements.Maya.Exact) != "" || strings.TrimSpace(scenario.Requirements.Maya.Minimum) != "") {
		return fmt.Errorf("Scenario cannot combine mayaVersion with requirements.maya") //nolint:staticcheck // Scenario is a product term.
	}
	for _, requirement := range []struct {
		name    string
		version versionRequirement
	}{
		{name: "Maya", version: scenario.Requirements.Maya},
		{name: "Python", version: scenario.Requirements.Python},
		{name: "Session Broker", version: versionRequirement{Exact: scenario.Requirements.SessionBroker.Exact, Minimum: scenario.Requirements.SessionBroker.Minimum}},
	} {
		if strings.TrimSpace(requirement.version.Exact) != "" && strings.TrimSpace(requirement.version.Minimum) != "" {
			return fmt.Errorf("%s requirement cannot set both exact and minimum", requirement.name)
		}
		if minimum := strings.TrimSpace(requirement.version.Minimum); minimum != "" {
			if _, valid := numericVersionParts(minimum); !valid {
				return fmt.Errorf("%s minimum version must be numeric", requirement.name)
			}
		}
	}
	return nil
}

func scenarioRequirementsForScheduling(repoDir string, scenarioName string) (scenarioRequirements, error) {
	config, _, err := loadRepoRunConfig(repoDir)
	if err != nil {
		return scenarioRequirements{}, fmt.Errorf("load Scenario requirements: %w", err)
	}
	scenario, ok := config.Scenarios[scenarioName]
	if !ok {
		return scenarioRequirements{}, fmt.Errorf("load Scenario requirements: unknown Scenario %q", scenarioName)
	}
	if err := validateScenarioRequirements(scenario); err != nil {
		return scenarioRequirements{}, fmt.Errorf("load Scenario requirements: %w", err)
	}
	return normalizedScenarioRequirements(scenario), nil
}

func hostAgentCapabilityRecord(options hostAgentRunOnceOptions, now time.Time) (mayaHostCapabilityRecord, error) {
	if options.HostConfig == "" {
		report := configuredMayaHostCapabilityRecord(mayaHostConfig{ID: options.HostID, Health: "healthy"}, now)
		report.TargetProfiles = []string{"default"}
		return report, nil
	}
	config, err := loadUserHostConfig(options.HostConfig)
	if err != nil {
		return mayaHostCapabilityRecord{}, fmt.Errorf("load Windows Host Agent Host config for capability reporting: %w", err)
	}
	var selected *mayaHostConfig
	profiles := make([]string, 0)
	profileNames := make([]string, 0, len(config.TargetProfiles))
	for profileName := range config.TargetProfiles {
		profileNames = append(profileNames, profileName)
	}
	sort.Strings(profileNames)
	for _, profileName := range profileNames {
		profile := config.TargetProfiles[profileName]
		pool, ok := config.HostPools[profile.HostPool]
		if !ok {
			continue
		}
		for index := range pool.Hosts {
			if pool.Hosts[index].ID != options.HostID {
				continue
			}
			host := pool.Hosts[index]
			if selected == nil {
				selected = &host
			} else if !reflect.DeepEqual(*selected, host) {
				return mayaHostCapabilityRecord{}, fmt.Errorf("Maya Host %q has conflicting definitions in Target Profile Host Pools", options.HostID) //nolint:staticcheck // Product terms start the user-facing diagnostic.
			}
			profiles = append(profiles, profileName)
			break
		}
	}
	if selected == nil {
		return mayaHostCapabilityRecord{}, fmt.Errorf("Maya Host %q is not in any Target Profile Host Pool", options.HostID) //nolint:staticcheck // Product terms start the user-facing diagnostic.
	}
	if _, err := resolveLiveHostAgentRuntime(*selected); err != nil {
		return mayaHostCapabilityRecord{}, fmt.Errorf("validate Windows Host Agent runtime for capability reporting: %w", err) //nolint:staticcheck // Product term starts the user-facing diagnostic.
	}
	sort.Strings(profiles)
	report := configuredMayaHostCapabilityRecord(*selected, now)
	report.TargetProfiles = profiles
	return report, nil
}

func configuredMayaHostCapabilityRecord(host mayaHostConfig, now time.Time) mayaHostCapabilityRecord {
	capabilities := inferredMayaHostCapabilities(host)
	if host.Capabilities != nil {
		capabilities = *host.Capabilities
	}
	health := strings.ToLower(strings.TrimSpace(host.Health))
	if health == "" {
		health = "healthy"
	}
	return mayaHostCapabilityRecord{
		Version: mayaHostCapabilityRecordVersion, ReportedAt: now.UTC().Format(time.RFC3339Nano), Online: health != "offline",
		Health: health, Maintenance: health == "maintenance" || health == "maintained", Quarantined: health == "quarantined",
		Capabilities: capabilities,
	}
}

func snapshotMayaHostCapabilityRecord(report mayaHostCapabilityRecord) mayaHostCapabilityRecord {
	snapshot := report
	snapshot.TargetProfiles = append([]string(nil), report.TargetProfiles...)
	snapshot.Capabilities.MayaBuilds = append([]string(nil), report.Capabilities.MayaBuilds...)
	snapshot.Capabilities.SessionBroker.Features = append([]string(nil), report.Capabilities.SessionBroker.Features...)
	snapshot.Capabilities.Capture = append([]string(nil), report.Capabilities.Capture...)
	snapshot.Capabilities.Control = append([]string(nil), report.Capabilities.Control...)
	snapshot.Capabilities.Renderers = append([]string(nil), report.Capabilities.Renderers...)
	snapshot.Capabilities.GPU = append([]string(nil), report.Capabilities.GPU...)
	snapshot.Capabilities.Display = append([]string(nil), report.Capabilities.Display...)
	snapshot.Capabilities.Licensing = append([]string(nil), report.Capabilities.Licensing...)
	if report.Capabilities.TrustedPluginArtifacts != nil {
		trusted := *report.Capabilities.TrustedPluginArtifacts
		snapshot.Capabilities.TrustedPluginArtifacts = &trusted
	}
	return snapshot
}

func inferredMayaHostCapabilities(host mayaHostConfig) mayaHostCapabilities {
	trusted := strings.TrimSpace(host.TrustedPluginArtifactsRoot) != ""
	capture := []string{}
	control := []string{}
	if host.VisualEvidence == nil || *host.VisualEvidence {
		capture = append(capture, "screenshot", "recording", "visual-evidence")
	}
	if host.usesRealSSH() {
		control = append(control, "coordinate")
	}
	mayaBuilds := staticMayaVersions(host)
	sessionMayaBuild := ""
	if !host.usesRealSSH() && len(mayaBuilds) == 1 {
		// The controlled fake runtime is exact; real inventory names can be coarse product families.
		sessionMayaBuild = mayaBuilds[0]
	}
	return mayaHostCapabilities{
		MayaBuilds: mayaBuilds, SessionMayaBuild: sessionMayaBuild, Python: "unknown",
		SessionBroker: sessionBrokerCapabilities{Version: "1", Features: []string{"script.execute"}},
		Capture:       capture, Control: control, Renderers: []string{"unknown"}, GPU: []string{"unknown"},
		Display: []string{"unknown"}, Licensing: []string{"unknown"}, TrustedPluginArtifacts: &trusted,
	}
}

func decideMayaHostCompatibility(requirements scenarioRequirements, report mayaHostCapabilityRecord, now time.Time) hostCompatibilityDecision {
	reasons := capabilityEligibilityReasons(report, now)
	selectedMayaBuild, mayaCompatible := selectMayaBuild([]string{report.Capabilities.SessionMayaBuild}, requirements.Maya)
	if requirements.Maya.Exact != "" && !mayaCompatible {
		reasons = append(reasons, fmt.Sprintf("Scenario requires exact Maya build %s; reported session build is %s; installed builds are %s", requirements.Maya.Exact, report.Capabilities.SessionMayaBuild, capabilityValues(report.Capabilities.MayaBuilds)))
	}
	if requirements.Maya.Minimum != "" && !mayaCompatible {
		reasons = append(reasons, fmt.Sprintf("Maya requires minimum %s; reported session build is %s; installed builds are %s", requirements.Maya.Minimum, report.Capabilities.SessionMayaBuild, capabilityValues(report.Capabilities.MayaBuilds)))
	}
	reasons = append(reasons, versionCompatibilityReasons("Python", requirements.Python, report.Capabilities.Python)...)
	reasons = append(reasons, versionCompatibilityReasons("Session Broker", versionRequirement{Exact: requirements.SessionBroker.Exact, Minimum: requirements.SessionBroker.Minimum}, report.Capabilities.SessionBroker.Version)...)
	reasons = append(reasons, requiredCapabilityReasons("Session Broker feature", requirements.SessionBroker.Features, report.Capabilities.SessionBroker.Features)...)
	reasons = append(reasons, requiredCapabilityReasons("capture capability", requirements.Capture, report.Capabilities.Capture)...)
	reasons = append(reasons, requiredCapabilityReasons("control capability", requirements.Control, report.Capabilities.Control)...)
	reasons = append(reasons, requiredCapabilityReasons("renderer capability", requirements.Renderers, report.Capabilities.Renderers)...)
	reasons = append(reasons, requiredCapabilityReasons("GPU capability", requirements.GPU, report.Capabilities.GPU)...)
	reasons = append(reasons, requiredCapabilityReasons("display capability", requirements.Display, report.Capabilities.Display)...)
	reasons = append(reasons, requiredCapabilityReasons("licensing capability", requirements.Licensing, report.Capabilities.Licensing)...)
	if requirements.TrustedPluginArtifacts != nil && (report.Capabilities.TrustedPluginArtifacts == nil || *report.Capabilities.TrustedPluginArtifacts != *requirements.TrustedPluginArtifacts) {
		reasons = append(reasons, fmt.Sprintf("trusted Plugin Artifact support must be %t", *requirements.TrustedPluginArtifacts))
	}
	return hostCompatibilityDecision{Compatible: len(reasons) == 0, Reasons: reasons, SelectedMayaBuild: selectedMayaBuild}
}

func selectMayaBuild(reported []string, requirement versionRequirement) (string, bool) {
	if requirement.Exact != "" {
		for _, build := range reported {
			if sameMayaBuildVersion(build, requirement.Exact) {
				return strings.TrimSpace(build), true
			}
		}
		return "", false
	}
	if requirement.Minimum == "" {
		return "", true
	}
	compatible := make([]string, 0, len(reported))
	for _, build := range reported {
		if versionAtLeast(build, requirement.Minimum) {
			compatible = append(compatible, strings.TrimSpace(build))
		}
	}
	if len(compatible) == 0 {
		return "", false
	}
	sort.Slice(compatible, func(left, right int) bool {
		return compareNumericVersions(compatible[left], compatible[right]) < 0
	})
	return compatible[0], true
}

func compatibleHostAgentCandidates(agents map[string]*controlPlaneHostAgent, targetProfile string, requirements scenarioRequirements, now time.Time) ([]*controlPlaneHostAgent, []string) {
	ordered := make([]*controlPlaneHostAgent, 0, len(agents))
	for _, agent := range agents {
		if agent != nil {
			ordered = append(ordered, agent)
		}
	}
	// Host identity, not map iteration or Agent identity, defines scheduling order.
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].status.HostID != ordered[right].status.HostID {
			return ordered[left].status.HostID < ordered[right].status.HostID
		}
		return ordered[left].status.AgentID < ordered[right].status.AgentID
	})
	candidates := make([]*controlPlaneHostAgent, 0, len(ordered))
	reasons := make([]string, 0)
	for _, agent := range ordered {
		if agent.status.State != "ready" || agent.status.RunID != "" || agent.status.SessionID == "" || agent.status.Slots != 1 || !agent.status.SessionBinding {
			continue
		}
		if !containsExactString(agent.status.Capabilities.TargetProfiles, targetProfile) {
			reasons = append(reasons, fmt.Sprintf("Maya Host %s is not in Target Profile %s", agent.status.HostID, targetProfile))
			continue
		}
		decision := decideMayaHostCompatibility(requirements, agent.status.Capabilities, now)
		if !decision.Compatible {
			for _, reason := range decision.Reasons {
				reasons = append(reasons, fmt.Sprintf("Maya Host %s: %s", agent.status.HostID, reason))
			}
			continue
		}
		candidates = append(candidates, agent)
	}
	return candidates, reasons
}

func containsExactString(values []string, required string) bool {
	for _, value := range values {
		if value == required {
			return true
		}
	}
	return false
}

func requiredCapabilityReasons(label string, required []string, reported []string) []string {
	reasons := make([]string, 0)
	for _, capability := range required {
		if !containsCapability(reported, capability) {
			reasons = append(reasons, fmt.Sprintf("%s %s is required; reported values are %s", label, capability, capabilityValues(reported)))
		}
	}
	return reasons
}

func versionCompatibilityReasons(name string, requirement versionRequirement, reported string) []string {
	if requirement.Exact != "" && strings.TrimSpace(reported) != strings.TrimSpace(requirement.Exact) {
		return []string{fmt.Sprintf("%s requires exact %s; reported version is %s", name, requirement.Exact, reported)}
	}
	if requirement.Minimum != "" && !versionAtLeast(reported, requirement.Minimum) {
		return []string{fmt.Sprintf("%s requires minimum %s; reported version is %s", name, requirement.Minimum, reported)}
	}
	return nil
}

func versionAtLeast(reported string, minimum string) bool {
	return compareNumericVersions(reported, minimum) >= 0
}

func compareNumericVersions(left string, right string) int {
	leftParts, leftOK := numericVersionParts(left)
	rightParts, rightOK := numericVersionParts(right)
	if !leftOK || !rightOK {
		return -1
	}
	length := len(leftParts)
	if len(rightParts) > length {
		length = len(rightParts)
	}
	for index := 0; index < length; index++ {
		var leftValue, rightValue int
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func sameMayaBuildVersion(left string, right string) bool {
	_, leftOK := numericVersionParts(left)
	_, rightOK := numericVersionParts(right)
	if leftOK && rightOK {
		return compareNumericVersions(left, right) == 0
	}
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}

func numericVersionParts(version string) ([]int, bool) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) == 0 {
		return nil, false
	}
	parsed := make([]int, len(parts))
	for index, part := range parts {
		if part == "" {
			return nil, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return nil, false
		}
		parsed[index] = value
	}
	return parsed, true
}

func capabilityEligibilityReasons(report mayaHostCapabilityRecord, now time.Time) []string {
	var reasons []string
	if report.Version != mayaHostCapabilityRecordVersion {
		reasons = append(reasons, fmt.Sprintf("capability record version is %d; required version is %d", report.Version, mayaHostCapabilityRecordVersion))
	}
	reportedAt, err := time.Parse(time.RFC3339Nano, report.ReportedAt)
	if err != nil {
		reasons = append(reasons, "capability record timestamp is missing or invalid")
	} else if age := now.Sub(reportedAt); age < -mayaHostCapabilityClockSkew {
		reasons = append(reasons, "capability record timestamp is too far in the future")
	} else if age > mayaHostCapabilityFreshness {
		reasons = append(reasons, "capability record is stale")
	}
	if !report.Online {
		reasons = append(reasons, "Maya Host is offline")
	}
	if report.Health != "healthy" {
		reasons = append(reasons, "Maya Host health is "+report.Health)
	}
	if report.Maintenance {
		reasons = append(reasons, "Maya Host is under maintenance")
	}
	if report.Quarantined {
		reasons = append(reasons, "Maya Host is quarantined")
	}
	if report.Capabilities.SessionMayaBuild != "" && !containsCapability(report.Capabilities.MayaBuilds, report.Capabilities.SessionMayaBuild) {
		reasons = append(reasons, "capability record session Maya build is not in reported Maya builds")
	}
	reasons = append(reasons, incompleteCapabilityReasons(report.Capabilities)...)
	return reasons
}

func incompleteCapabilityReasons(capabilities mayaHostCapabilities) []string {
	missing := make([]string, 0)
	if len(capabilities.MayaBuilds) == 0 {
		missing = append(missing, "Maya builds")
	}
	if strings.TrimSpace(capabilities.SessionMayaBuild) == "" {
		missing = append(missing, "session Maya build")
	}
	if strings.TrimSpace(capabilities.Python) == "" {
		missing = append(missing, "Python")
	}
	if strings.TrimSpace(capabilities.SessionBroker.Version) == "" || capabilities.SessionBroker.Features == nil {
		missing = append(missing, "Session Broker")
	}
	// Nil means unreported; an empty slice explicitly reports that the Host supports none.
	for name, values := range map[string][]string{
		"capture": capabilities.Capture, "control": capabilities.Control, "renderer": capabilities.Renderers,
		"GPU": capabilities.GPU, "display": capabilities.Display, "licensing": capabilities.Licensing,
	} {
		if values == nil {
			missing = append(missing, name)
		}
	}
	if capabilities.TrustedPluginArtifacts == nil {
		missing = append(missing, "trusted Plugin Artifact support")
	}
	sort.Strings(missing)
	reasons := make([]string, 0, len(missing))
	for _, name := range missing {
		reasons = append(reasons, "capability record is incomplete: missing "+name)
	}
	return reasons
}

func containsCapability(values []string, required string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(required)) {
			return true
		}
	}
	return false
}

func capabilityValues(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}
