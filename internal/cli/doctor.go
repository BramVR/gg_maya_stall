package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type doctorReport struct {
	Checks  []doctorCheck
	Healthy bool
}

type doctorCheck struct {
	Layer  string
	Status string
	Detail string
	Hint   string
}

func runDoctor(repoDir string, options doctorOptions) doctorReport {
	report := doctorReport{Healthy: true}
	add := func(check doctorCheck) {
		if check.Status == "fail" {
			report.Healthy = false
		}
		report.Checks = append(report.Checks, check)
	}

	config, configPath, err := loadRepoRunConfig(repoDir)
	if err != nil {
		add(failedCheck("local-config", err.Error(), "Run maya-stall init or fix the repo config schema."))
		return report
	}
	add(okCheck("local-config", repoRelativePath(repoDir, configPath)))

	var scenario scenarioConfig
	if options.ScenarioName != "" {
		selected, ok := config.Scenarios[options.ScenarioName]
		if !ok {
			add(failedCheck("scenario-inputs", fmt.Sprintf("unknown Scenario %q", options.ScenarioName), "Choose a configured Scenario or add it to the repo config. See docs/setup/windows-maya-host.md#scenario-inputs."))
		} else {
			scenario = selected
			if err := validateScenarioInputs(repoDir, options.ScenarioName, scenario); err != nil {
				add(failedCheck("scenario-inputs", err.Error(), "Fix the Scenario payload paths and expectedOutputs.scenarioResult in repo config. See docs/setup/windows-maya-host.md#scenario-inputs."))
			} else {
				add(okCheck("scenario-inputs", options.ScenarioName))
			}
		}
	}

	hostConfig, err := loadUserHostConfig(options.HostConfig)
	if err != nil {
		add(failedCheck("target-profile", err.Error(), "Point --host-config at a valid user host config or omit it for the fake default. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	profile, ok := hostConfig.TargetProfiles[options.TargetProfile]
	if !ok {
		add(failedCheck("target-profile", fmt.Sprintf("unknown Target Profile %q", options.TargetProfile), "Choose a configured Target Profile or add it to the host config. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	add(okCheck("target-profile", options.TargetProfile))

	pool, ok := hostConfig.HostPools[profile.HostPool]
	if !ok {
		add(failedCheck("host-pool", fmt.Sprintf("unknown Host Pool %q", profile.HostPool), "Fix the Target Profile hostPool reference in the host config. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	if len(pool.Hosts) == 0 {
		add(failedCheck("host-pool", fmt.Sprintf("%s has no Maya Hosts", profile.HostPool), "Add at least one Maya Host to the Host Pool. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	for _, host := range pool.Hosts {
		if err := validateHostID(host.ID); err != nil {
			add(failedCheck("host-pool", err.Error(), "Fix Maya Host ids in the Host Pool config. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
			return report
		}
	}
	if !hostPoolHasHealthyHost(pool.Hosts) {
		add(failedCheck("host-pool", fmt.Sprintf("%s has no healthy Maya Hosts", profile.HostPool), "Mark a ready Maya Host healthy or repair one host before running. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	add(okCheck("host-pool", profile.HostPool))

	if options.HostPin == "" && options.ScenarioName == "" && !hostPoolHasRealSSHHost(pool.Hosts) {
		return report
	}
	host, err := selectDoctorHost(pool.Hosts, options.HostPin)
	if err != nil {
		add(failedCheck("host", err.Error(), "Choose a Maya Host from the selected Target Profile or repair Host Pool config. See docs/setup/windows-maya-host.md#target-profile-and-host-pool."))
		return report
	}
	if options.HostPin == "" && options.ScenarioName == "" {
		if realSSHHost, ok := firstHealthyRealSSHHost(pool.Hosts); ok {
			host = realSSHHost
		}
	}
	add(okCheck("host", host.ID))
	checkHostLayers(repoDir, options, host, scenario, add)

	return report
}

func validateScenarioInputs(repoDir string, scenarioName string, scenario scenarioConfig) error {
	if scenario.ExpectedOutputs.ScenarioResult == "" {
		return fmt.Errorf("Scenario %q missing expectedOutputs.scenarioResult", scenarioName)
	}
	if _, err := cleanRepoRelativePath(scenario.ExpectedOutputs.ScenarioResult); err != nil {
		return err
	}
	payload, err := buildManifestPayload(scenario.Payload)
	if err != nil {
		return err
	}
	for _, item := range payload {
		if err := ensurePayloadPathHasNoSymlinkAncestor(repoDir, item.Source); err != nil {
			return fmt.Errorf("%s payload %s: %w", item.Kind, item.Source, err)
		}
	}
	return nil
}

func hostPoolHasHealthyHost(hosts []mayaHostConfig) bool {
	for _, host := range hosts {
		if isHealthyHost(host) {
			return true
		}
	}
	return false
}

func hostPoolHasRealSSHHost(hosts []mayaHostConfig) bool {
	_, ok := firstHealthyRealSSHHost(hosts)
	return ok
}

func firstHealthyRealSSHHost(hosts []mayaHostConfig) (mayaHostConfig, bool) {
	for _, host := range hosts {
		if isHealthyHost(host) && host.usesRealSSH() {
			return host, true
		}
	}
	return mayaHostConfig{}, false
}

func selectDoctorHost(hosts []mayaHostConfig, hostPin string) (mayaHostConfig, error) {
	for _, host := range hosts {
		if err := validateHostID(host.ID); err != nil {
			return mayaHostConfig{}, err
		}
		if hostPin != "" {
			if host.ID == hostPin {
				return host, nil
			}
			continue
		}
		if isHealthyHost(host) {
			return host, nil
		}
	}
	if hostPin != "" {
		return mayaHostConfig{}, fmt.Errorf("pinned Maya Host %q is not in Target Profile", hostPin)
	}
	return mayaHostConfig{}, fmt.Errorf("no healthy Maya Host available")
}

func checkHostLayers(repoDir string, options doctorOptions, host mayaHostConfig, scenario scenarioConfig, add func(doctorCheck)) {
	if host.usesRealSSH() {
		add(realSSHLayer(host))
		add(realWorkRootLayer(host))
	} else {
		add(statusLayer("fake-ssh", host.SSH.FakeStatus, "reachable", []string{"", "ok", "healthy", "reachable"}, "Fix SSH reachability for this Maya Host. See docs/setup/windows-maya-host.md#openssh-reachability."))
		add(statusLayer("work-root", host.WorkRoot, "writable", []string{"", "ok", "writable"}, "Fix the host work root path or permissions. See docs/setup/windows-maya-host.md#work-root."))
	}
	lockCheck := hostLockLayer(repoDir, host.ID)
	add(lockCheck)
	resolved, runtimeErr := resolveRuntimeForHost(host)
	if runtimeErr != nil {
		add(failedCheck("runtime", runtimeErr.Error(), "Choose a supported runtime profile: fake-local or ssh-sessiond. See docs/setup/windows-maya-host.md#session-broker."))
	} else {
		add(okCheck("runtime", resolved.Metadata.Profile))
	}
	brokerInvalidReason := host.Broker.invalidReason()
	if runtimeErr != nil {
		brokerInvalidReason = runtimeErr.Error()
	}
	brokerLayerInvalidReason := ""
	if runtimeErr != nil || host.Broker.missingStructuredType() || strings.TrimSpace(host.Broker.Type) != "" {
		brokerLayerInvalidReason = brokerInvalidReason
	}
	sessionBrokerOK := false
	probeLockDetail := ""
	if runtimeErr == nil && host.Broker.isGGMayaSessiond() && lockCheck.Status == "ok" {
		release, locked, err := acquireHostLock(repoDir, host.ID)
		if err != nil {
			probeLockDetail = err.Error()
		} else if locked {
			probeLockDetail = fmt.Sprintf("%s locked", host.ID)
		} else {
			defer func() {
				if err := release(); err != nil {
					add(failedCheck("host-lock", fmt.Sprintf("release doctor probe lock for %s: %v", host.ID, err), "Inspect the Host Lock state directory and permissions. See docs/setup/windows-maya-host.md#host-lock-and-retention."))
				}
			}()
		}
	}
	if runtimeErr == nil && host.Broker.isGGMayaSessiond() {
		broker := resolved.Broker.(ggMayaSessiondBroker)
		var check doctorCheck
		if err := broker.validate(); err != nil {
			check = failedCheck("session-broker", err.Error(), "Configure gg_mayasessiond paths in host config. See docs/setup/windows-maya-host.md#session-broker.")
		} else if probeLockDetail != "" {
			check = failedCheck("session-broker", "skipped because Host Lock is not clear: "+probeLockDetail, "Wait for the active Fresh Run or clear the stale Host Lock before probing gg_mayasessiond. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
		} else if lockCheck.Status == "ok" {
			check = realSessionBrokerLayer(host)
		} else {
			check = failedCheck("session-broker", "skipped because Host Lock is not clear", "Wait for the active Fresh Run or clear the stale Host Lock before probing gg_mayasessiond. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
		}
		sessionBrokerOK = check.Status == "ok"
		add(check)
	} else if brokerLayerInvalidReason != "" {
		add(failedCheck("session-broker", brokerLayerInvalidReason, "Use broker.type: gg-mayasessiond or a legacy fake broker status. See docs/setup/windows-maya-host.md#session-broker."))
	} else {
		add(statusLayer("session-broker", host.Broker.fakeStatus(), "reachable", []string{"", "ok", "healthy", "reachable"}, "Start or repair the Session Broker on this Maya Host. See docs/setup/windows-maya-host.md#session-broker."))
	}
	add(mayaVersionLayer(options, host, scenario))
	if host.VisualEvidence != nil && !*host.VisualEvidence {
		add(failedCheck("visual-evidence", "unavailable", "Enable screenshot or recording capture through the Session Broker. See docs/setup/windows-maya-host.md#visual-evidence."))
	} else if brokerInvalidReason != "" {
		add(failedCheck("visual-evidence", "unavailable: "+brokerInvalidReason, "Use a valid Session Broker before checking screenshot or recording capture. See docs/setup/windows-maya-host.md#visual-evidence."))
	} else if runtimeErr == nil && host.Broker.isGGMayaSessiond() && scenario.Evidence.Recording.Enabled {
		add(failedCheck("visual-evidence", "gg_mayasessiond recording capture unsupported", "Disable recording evidence or use screenshot/viewport capture. See docs/setup/windows-maya-host.md#visual-evidence."))
	} else if runtimeErr == nil && host.Broker.isGGMayaSessiond() {
		broker := resolved.Broker.(ggMayaSessiondBroker)
		if err := broker.validate(); err != nil {
			add(failedCheck("visual-evidence", "unavailable: "+err.Error(), "Configure gg_mayasessiond paths before checking screenshot capture. See docs/setup/windows-maya-host.md#visual-evidence."))
		} else if probeLockDetail != "" {
			add(failedCheck("visual-evidence", "skipped because Host Lock is not clear: "+probeLockDetail, "Wait for the active Fresh Run or clear the stale Host Lock before probing viewport.capture. See docs/setup/windows-maya-host.md#host-lock-and-retention."))
		} else if lockCheck.Status != "ok" {
			add(failedCheck("visual-evidence", "skipped because Host Lock is not clear", "Wait for the active Fresh Run or clear the stale Host Lock before probing viewport.capture. See docs/setup/windows-maya-host.md#host-lock-and-retention."))
		} else if !sessionBrokerOK {
			add(failedCheck("visual-evidence", "skipped because session-broker is not healthy", "Repair the gg_mayasessiond session-broker layer before probing viewport.capture. See docs/setup/windows-maya-host.md#visual-evidence."))
		} else {
			add(realSessionBrokerVisualEvidenceLayer(host))
		}
	} else {
		add(okCheck("visual-evidence", "available"))
	}
}

type sessiondDoctorOutput struct {
	OK     bool `json:"ok"`
	Checks []struct {
		ID string `json:"id"`
		OK bool   `json:"ok"`
	} `json:"checks"`
}

type sessiondStatusOutput struct {
	State struct {
		Status          string `json:"status"`
		MayaAlive       bool   `json:"maya_alive"`
		MCPAlive        bool   `json:"mcp_alive"`
		CallServerReady bool   `json:"call_server_ready"`
	} `json:"state"`
	DerivedStatus string `json:"derived_status"`
}

type windowsProcessSession struct {
	ProcessID int    `json:"ProcessId"`
	SessionID int    `json:"SessionId"`
	Name      string `json:"Name"`
}

func realSessionBrokerLayer(host mayaHostConfig) doctorCheck {
	broker := ggMayaSessiondBroker{host: host}
	if err := broker.validate(); err != nil {
		return failedCheck("session-broker", err.Error(), "Configure gg_mayasessiond paths in host config. See docs/setup/windows-maya-host.md#session-broker.")
	}
	doctorRaw, err := broker.runSessiondCLI(sessiondDoctorArgs(host), sessiondCommandTimeout)
	if err != nil {
		return failedCheck("session-broker", err.Error(), "Start or repair gg_mayasessiond on this Maya Host. See docs/setup/windows-maya-host.md#session-broker.")
	}
	var doctor sessiondDoctorOutput
	if err := json.Unmarshal(doctorRaw, &doctor); err != nil {
		return failedCheck("session-broker", "invalid doctor JSON", "Update gg_mayasessiond or fix its CLI JSON output. See docs/setup/windows-maya-host.md#session-broker.")
	}
	if !doctor.OK {
		detail := "gg_mayasessiond doctor failed"
		if failing := failingSessiondDoctorChecks(doctor); len(failing) > 0 {
			detail += ": " + strings.Join(failing, ", ")
		}
		return failedCheck("session-broker", detail, "Repair the failing gg_mayasessiond doctor check. See docs/setup/windows-maya-host.md#session-broker.")
	}
	statusRaw, err := broker.runSessiondCLI([]string{"status", "--state-dir", host.Broker.StateDir, "--json"}, sessiondCommandTimeout)
	if err != nil {
		return failedCheck("session-broker", err.Error(), "Start or repair gg_mayasessiond on this Maya Host. See docs/setup/windows-maya-host.md#session-broker.")
	}
	var status sessiondStatusOutput
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		return failedCheck("session-broker", "invalid status JSON", "Update gg_mayasessiond or fix its CLI JSON output. See docs/setup/windows-maya-host.md#session-broker.")
	}
	effectiveStatus := status.DerivedStatus
	if effectiveStatus == "" {
		effectiveStatus = status.State.Status
	}
	if effectiveStatus != "running" {
		return failedCheck("session-broker", "gg_mayasessiond is not running", "Start the interactive gg_mayasessiond broker. See docs/setup/windows-maya-host.md#session-broker.")
	}
	if !status.State.CallServerReady {
		return failedCheck("session-broker", "gg_mayasessiond call server is not ready", "Repair Maya MCP startup for gg_mayasessiond. See docs/setup/windows-maya-host.md#session-broker.")
	}
	processes, err := mayaProcessSessions(host)
	if err != nil {
		return failedCheck("session-broker", err.Error(), "Check Maya process state from the Windows host. See docs/setup/windows-maya-host.md#interactive-desktop.")
	}
	if len(processes) == 0 {
		return failedCheck("session-broker", "maya.exe is not running", "Start gg_mayasessiond from the interactive Windows desktop. See docs/setup/windows-maya-host.md#interactive-desktop.")
	}
	for _, process := range processes {
		if process.SessionID == 0 {
			return failedCheck("session-broker", "maya.exe is running in Windows Services session 0", "Restart gg_mayasessiond from the interactive Windows desktop. See docs/setup/windows-maya-host.md#interactive-desktop.")
		}
	}
	if err := broker.probeScriptExecute(); err != nil {
		return failedCheck("session-broker", err.Error(), "Repair gg_mayasessiond script.execute access to the Maya Stall work root. See docs/setup/windows-maya-host.md#session-broker.")
	}
	return okCheck("session-broker", "gg_mayasessiond reachable; Maya UI is interactive")
}

func failingSessiondDoctorChecks(doctor sessiondDoctorOutput) []string {
	var failing []string
	for _, check := range doctor.Checks {
		if !check.OK && check.ID != "" {
			failing = append(failing, check.ID)
		}
	}
	return failing
}

func realSessionBrokerVisualEvidenceLayer(host mayaHostConfig) doctorCheck {
	broker := ggMayaSessiondBroker{host: host}
	result, err := broker.callCapture()
	if err != nil {
		return failedCheck("visual-evidence", err.Error(), "Repair viewport.capture in gg_mayasessiond. See docs/setup/windows-maya-host.md#visual-evidence.")
	}
	if !result.OK {
		return failedCheck("visual-evidence", result.Error, "Repair viewport.capture in gg_mayasessiond. See docs/setup/windows-maya-host.md#visual-evidence.")
	}
	if _, _, err := captureImageData(result); err != nil {
		return failedCheck("visual-evidence", err.Error(), "Repair viewport.capture in gg_mayasessiond. See docs/setup/windows-maya-host.md#visual-evidence.")
	}
	return okCheck("visual-evidence", "viewport.capture available")
}

func sessiondDoctorArgs(host mayaHostConfig) []string {
	args := []string{"doctor", "--state-dir", host.Broker.StateDir}
	if host.Broker.MCPSource != "" {
		args = append(args, "--mcp-src", host.Broker.MCPSource)
	}
	args = append(args, "--json")
	return args
}

func mayaProcessSessions(host mayaHostConfig) ([]windowsProcessSession, error) {
	script := `$ErrorActionPreference = 'Stop'
Get-CimInstance Win32_Process -Filter "Name = 'maya.exe'" | Select-Object ProcessId,SessionId,Name | ConvertTo-Json -Compress`
	raw, err := runSSHCommandOutput(host, encodedPowerShellCommand(script), sessiondCommandTimeout)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var processes []windowsProcessSession
		if err := json.Unmarshal([]byte(trimmed), &processes); err != nil {
			return nil, fmt.Errorf("parse maya.exe process JSON: %w", err)
		}
		return processes, nil
	}
	var process windowsProcessSession
	if err := json.Unmarshal([]byte(trimmed), &process); err != nil {
		return nil, fmt.Errorf("parse maya.exe process JSON: %w", err)
	}
	return []windowsProcessSession{process}, nil
}

func statusLayer(layer string, value string, okDetail string, okValues []string, hint string) doctorCheck {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, okValue := range okValues {
		if normalized == okValue {
			return okCheck(layer, okDetail)
		}
	}
	if normalized == "" {
		normalized = "unknown"
	}
	return failedCheck(layer, normalized, hint)
}

func mayaVersionLayer(options doctorOptions, host mayaHostConfig, scenario scenarioConfig) doctorCheck {
	versions := host.MayaVersions
	if len(versions) == 0 {
		versions = []string{"2025"}
	}
	installed := strings.Join(versions, ",")
	if options.ScenarioName == "" || scenario.MayaVersion == "" {
		return okCheck("maya-version", installed)
	}
	for _, version := range versions {
		if version == scenario.MayaVersion {
			return okCheck("maya-version", fmt.Sprintf("%s satisfies Scenario %s", version, options.ScenarioName))
		}
	}
	return failedCheck("maya-version", fmt.Sprintf("Scenario %s needs %s; host has %s", options.ScenarioName, scenario.MayaVersion, installed), "Install a compatible Autodesk Maya version or choose another Maya Host. See docs/setup/windows-maya-host.md#autodesk-maya.")
}

func hostLockLayer(repoDir string, hostID string) doctorCheck {
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", hostID+".lock")
	_, err := os.Stat(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return okCheck("host-lock", "unlocked")
	}
	if err != nil {
		return failedCheck("host-lock", err.Error(), "Inspect the Host Lock state directory and permissions. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
	}
	stale, err := isStaleHostLock(lockPath)
	if err != nil {
		return failedCheck("host-lock", err.Error(), "Inspect or remove the unreadable Host Lock file. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
	}
	if stale {
		return okCheck("host-lock", "unlocked")
	}
	return failedCheck("host-lock", fmt.Sprintf("%s locked", hostID), "Wait for the active Fresh Run or clear the stale Host Lock after verifying no run is active. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
}

func okCheck(layer string, detail string) doctorCheck {
	return doctorCheck{Layer: layer, Status: "ok", Detail: detail}
}

func failedCheck(layer string, detail string, hint string) doctorCheck {
	return doctorCheck{Layer: layer, Status: "fail", Detail: detail, Hint: hint}
}
