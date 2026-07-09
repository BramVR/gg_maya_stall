package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultFakeHostID = "fake-local"

var hostIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type hostRuntime struct {
	TargetProfile string
	HostID        string
	Config        mayaHostConfig
	release       func() error
}

type resolvedRuntime struct {
	Host     runHost
	Broker   sessionBroker
	Metadata runtimeMetadata
}

type runtimeMetadata struct {
	Profile            string `json:"profile"`
	HostAdapter        string `json:"hostAdapter"`
	BrokerAdapter      string `json:"brokerAdapter"`
	BrokerConfigSource string `json:"brokerConfigSource"`
	LiveProofEligible  bool   `json:"liveProofEligible"`
}

type userHostConfig struct {
	Version        int                            `yaml:"version"`
	TargetProfiles map[string]targetProfileConfig `yaml:"targetProfiles"`
	HostPools      map[string]hostPoolConfig      `yaml:"hostPools"`
}

type targetProfileConfig struct {
	HostPool string `yaml:"hostPool"`
}

type hostPoolConfig struct {
	Hosts []mayaHostConfig `yaml:"hosts"`
}

type mayaHostConfig struct {
	ID                         string       `yaml:"id"`
	Health                     string       `yaml:"health"`
	Transport                  string       `yaml:"transport"`
	SSH                        sshConfig    `yaml:"ssh"`
	WorkRoot                   string       `yaml:"workRoot"`
	TrustedPluginArtifactsRoot string       `yaml:"trustedPluginArtifactsRoot"`
	Broker                     brokerConfig `yaml:"broker"`
	MayaVersions               []string     `yaml:"mayaVersions"`
	VisualEvidence             *bool        `yaml:"visualEvidence"`
}

type brokerConfig struct {
	FakeStatus string `yaml:"-"`
	Structured bool   `yaml:"-"`
	Type       string `yaml:"type"`
	StateDir   string `yaml:"stateDir"`
	Python     string `yaml:"python"`
	Repo       string `yaml:"repo"`
	MCPSource  string `yaml:"mcpSource"`
}

func (config *brokerConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*config = brokerConfig{FakeStatus: value.Value}
		return nil
	}
	if err := rejectUnknownYAMLMappingFields(value, brokerConfigYAMLFields, "cli.brokerConfig"); err != nil {
		return err
	}
	var decoded struct {
		Type      string `yaml:"type"`
		StateDir  string `yaml:"stateDir"`
		Python    string `yaml:"python"`
		Repo      string `yaml:"repo"`
		MCPSource string `yaml:"mcpSource"`
	}
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*config = brokerConfig{
		Structured: true,
		Type:       decoded.Type,
		StateDir:   decoded.StateDir,
		Python:     decoded.Python,
		Repo:       decoded.Repo,
		MCPSource:  decoded.MCPSource,
	}
	return nil
}

func (config brokerConfig) isGGMayaSessiond() bool {
	return strings.EqualFold(strings.TrimSpace(config.Type), "gg-mayasessiond")
}

func (config brokerConfig) fakeStatus() string {
	return config.FakeStatus
}

func (config brokerConfig) isLegacyFakeStatus() bool {
	switch strings.ToLower(strings.TrimSpace(config.FakeStatus)) {
	case "", "ok", "healthy", "reachable":
		return true
	default:
		return false
	}
}

func (config brokerConfig) missingStructuredType() bool {
	return config.Structured && strings.TrimSpace(config.Type) == ""
}

func (config brokerConfig) invalidReason() string {
	if config.isGGMayaSessiond() {
		return ""
	}
	if config.missingStructuredType() {
		return "broker.type is required for structured broker config"
	}
	if strings.TrimSpace(config.Type) != "" {
		return fmt.Sprintf("unknown broker.type %q", config.Type)
	}
	if !config.isLegacyFakeStatus() {
		return fmt.Sprintf("broker status %q is not usable for runs", config.FakeStatus)
	}
	return ""
}

type sshConfig struct {
	FakeStatus   string
	Host         string `yaml:"host"`
	User         string `yaml:"user"`
	Port         int    `yaml:"port"`
	IdentityFile string `yaml:"identityFile"`
	Binary       string `yaml:"binary"`
	SFTPBinary   string `yaml:"sftpBinary"`
	SFTPTimeout  string `yaml:"sftpTimeout"`
}

func (config *sshConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*config = sshConfig{FakeStatus: value.Value}
		return nil
	}
	if err := rejectUnknownYAMLMappingFields(value, sshConfigYAMLFields, "cli.sshConfig"); err != nil {
		return err
	}
	var decoded struct {
		Host         string `yaml:"host"`
		User         string `yaml:"user"`
		Port         int    `yaml:"port"`
		IdentityFile string `yaml:"identityFile"`
		Binary       string `yaml:"binary"`
		SFTPBinary   string `yaml:"sftpBinary"`
		SFTPTimeout  string `yaml:"sftpTimeout"`
	}
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*config = sshConfig{
		Host:         decoded.Host,
		User:         decoded.User,
		Port:         decoded.Port,
		IdentityFile: decoded.IdentityFile,
		Binary:       decoded.Binary,
		SFTPBinary:   decoded.SFTPBinary,
		SFTPTimeout:  decoded.SFTPTimeout,
	}
	return nil
}

var brokerConfigYAMLFields = map[string]struct{}{
	"type":      {},
	"stateDir":  {},
	"python":    {},
	"repo":      {},
	"mcpSource": {},
}

var sshConfigYAMLFields = map[string]struct{}{
	"host":         {},
	"user":         {},
	"port":         {},
	"identityFile": {},
	"binary":       {},
	"sftpBinary":   {},
	"sftpTimeout":  {},
}

func rejectUnknownYAMLMappingFields(value *yaml.Node, known map[string]struct{}, typeName string) error {
	return rejectUnknownYAMLMappingFieldsWithStack(value, known, typeName, make(map[*yaml.Node]bool), make(map[*yaml.Node]bool))
}

func rejectUnknownYAMLMappingFieldsWithStack(value *yaml.Node, known map[string]struct{}, typeName string, visiting, validated map[*yaml.Node]bool) error {
	if value.Kind != yaml.MappingNode {
		return nil
	}
	if validated[value] {
		return nil
	}
	if visiting[value] {
		return fmt.Errorf("line %d: cyclic YAML merge in type %s", value.Line, typeName)
	}
	visiting[value] = true
	defer delete(visiting, value)
	for i := 0; i < len(value.Content); i += 2 {
		key := value.Content[i]
		if isYAMLMergeKey(key) {
			if err := rejectUnknownYAMLMergeFields(value.Content[i+1], known, typeName, visiting, validated); err != nil {
				return err
			}
			continue
		}
		if _, ok := known[key.Value]; !ok {
			return fmt.Errorf("line %d: field %s not found in type %s", key.Line, key.Value, typeName)
		}
	}
	validated[value] = true
	return nil
}

func isYAMLMergeKey(key *yaml.Node) bool {
	return key.Kind == yaml.ScalarNode && key.Value == "<<" &&
		(key.Tag == "" || key.Tag == "!" || key.ShortTag() == "!!merge")
}

func rejectUnknownYAMLMergeFields(value *yaml.Node, known map[string]struct{}, typeName string, visiting, validated map[*yaml.Node]bool) error {
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value == nil {
		return nil
	}
	if value.Kind == yaml.SequenceNode {
		if validated[value] {
			return nil
		}
		if visiting[value] {
			return fmt.Errorf("line %d: cyclic YAML merge in type %s", value.Line, typeName)
		}
		visiting[value] = true
		defer delete(visiting, value)
		for _, item := range value.Content {
			if err := rejectUnknownYAMLMergeFields(item, known, typeName, visiting, validated); err != nil {
				return err
			}
		}
		validated[value] = true
		return nil
	}
	return rejectUnknownYAMLMappingFieldsWithStack(value, known, typeName, visiting, validated)
}

func selectHostForRun(repoDir string, options runOptions) (hostRuntime, error) {
	config, err := loadUserHostConfig(options.HostConfig)
	if err != nil {
		return hostRuntime{}, err
	}
	if options.TargetProfile == "" {
		options.TargetProfile = "default"
	}
	candidates, err := hostCandidates(config, options.TargetProfile, options.HostPin)
	if err != nil {
		return hostRuntime{}, err
	}

	deadline := time.Now().Add(options.HostLockWait)
	for {
		var sawLocked bool
		for _, candidate := range candidates {
			if !isHealthyHost(candidate) {
				continue
			}
			release, locked, err := acquireHostLock(repoDir, candidate.ID)
			if err != nil {
				return hostRuntime{}, err
			}
			if locked {
				sawLocked = true
				continue
			}
			return hostRuntime{TargetProfile: options.TargetProfile, HostID: candidate.ID, Config: candidate, release: release}, nil
		}

		if options.HostLockWait <= 0 || time.Now().After(deadline) {
			if sawLocked {
				return hostRuntime{}, fmt.Errorf("no healthy unlocked Maya Host available in Target Profile %q", options.TargetProfile)
			}
			if options.HostPin != "" {
				return hostRuntime{}, fmt.Errorf("pinned Maya Host %q is not healthy in Target Profile %q", options.HostPin, options.TargetProfile)
			}
			return hostRuntime{}, fmt.Errorf("no healthy Maya Host available in Target Profile %q", options.TargetProfile)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func loadUserHostConfig(path string) (userHostConfig, error) {
	if path == "" {
		return userHostConfig{
			Version: 1,
			TargetProfiles: map[string]targetProfileConfig{
				"default": {HostPool: "default"},
			},
			HostPools: map[string]hostPoolConfig{
				"default": {Hosts: []mayaHostConfig{{ID: defaultFakeHostID, Health: "healthy"}}},
			},
		}, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return userHostConfig{}, err
	}
	var config userHostConfig
	if err := decodeKnownYAMLFields(content, &config); err != nil {
		return userHostConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if config.Version != 1 {
		return userHostConfig{}, fmt.Errorf("unsupported host config version %d", config.Version)
	}
	return config, nil
}

func hostCandidates(config userHostConfig, targetProfile string, hostPin string) ([]mayaHostConfig, error) {
	profile, ok := config.TargetProfiles[targetProfile]
	if !ok {
		return nil, newUsageError("unknown Target Profile %q", targetProfile)
	}
	pool, ok := config.HostPools[profile.HostPool]
	if !ok {
		return nil, fmt.Errorf("Target Profile %q references unknown Host Pool %q", targetProfile, profile.HostPool)
	}
	if len(pool.Hosts) == 0 {
		return nil, fmt.Errorf("Host Pool %q has no Maya Hosts", profile.HostPool)
	}
	for _, host := range pool.Hosts {
		if err := validateHostID(host.ID); err != nil {
			return nil, err
		}
	}
	if hostPin == "" {
		return pool.Hosts, nil
	}
	for _, host := range pool.Hosts {
		if host.ID == hostPin {
			return []mayaHostConfig{host}, nil
		}
	}
	return nil, newUsageError("pinned Maya Host %q is not in Target Profile %q", hostPin, targetProfile)
}

func validateHostID(id string) error {
	if id == "" {
		return fmt.Errorf("Maya Host id must not be empty")
	}
	if !hostIDPattern.MatchString(id) {
		return fmt.Errorf("Maya Host id %q must contain only letters, numbers, dots, underscores, or dashes", id)
	}
	return nil
}

func isHealthyHost(host mayaHostConfig) bool {
	health := strings.ToLower(strings.TrimSpace(host.Health))
	return health == "" || health == "healthy"
}

func (host mayaHostConfig) usesRealSSH() bool {
	return strings.EqualFold(strings.TrimSpace(host.Transport), "ssh") || host.SSH.Host != ""
}

func resolveRuntimeForHost(host mayaHostConfig) (resolvedRuntime, error) {
	if host.usesRealSSH() {
		if !host.Broker.isGGMayaSessiond() {
			if reason := host.Broker.invalidReason(); reason != "" {
				return resolvedRuntime{}, fmt.Errorf("%s", reason)
			}
			return resolvedRuntime{}, fmt.Errorf("SSH Maya Host requires broker.type: gg-mayasessiond")
		}
		broker := ggMayaSessiondBroker{host: host}
		if err := broker.validate(); err != nil {
			return resolvedRuntime{}, err
		}
		return resolvedRuntime{
			Host:   realSSHHost{host: host},
			Broker: broker,
			Metadata: runtimeMetadata{
				Profile:            "ssh-sessiond",
				HostAdapter:        "ssh",
				BrokerAdapter:      "gg-mayasessiond",
				BrokerConfigSource: "host config broker.type=gg-mayasessiond",
				LiveProofEligible:  true,
			},
		}, nil
	}

	if host.Broker.isGGMayaSessiond() {
		return resolvedRuntime{}, fmt.Errorf("fake Maya Host cannot use gg_mayasessiond Session Broker")
	}
	if reason := host.Broker.invalidReason(); reason != "" {
		return resolvedRuntime{}, fmt.Errorf("%s", reason)
	}
	return resolvedRuntime{
		Host:   fakeHost{},
		Broker: fakeSessionBroker{Result: ScenarioResult{Status: resultStatusPassed, Summary: "fake Scenario completed"}},
		Metadata: runtimeMetadata{
			Profile:            "fake-local",
			HostAdapter:        "fake",
			BrokerAdapter:      "fake",
			BrokerConfigSource: "default fake host config",
			LiveProofEligible:  false,
		},
	}, nil
}

func acquireHostLock(repoDir string, hostID string) (func() error, bool, error) {
	if err := validateHostID(hostID); err != nil {
		return nil, false, err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return nil, false, err
	}
	lockDir := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(lockDir, hostID+".lock")
	tempFile, err := os.CreateTemp(lockDir, hostID+".*.tmp")
	if err != nil {
		return nil, false, err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := fmt.Fprintf(tempFile, "host: %s\npid: %d\n", hostID, os.Getpid()); err != nil {
		tempFile.Close()
		return nil, false, err
	}
	if err := tempFile.Close(); err != nil {
		return nil, false, err
	}
	for {
		err := os.Link(tempPath, lockPath)
		if errors.Is(err, os.ErrExist) {
			stale, err := isStaleHostLock(lockPath)
			if err != nil {
				return nil, false, err
			}
			if !stale {
				return nil, true, nil
			}
			if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, false, err
			}
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return func() error {
			return os.Remove(lockPath)
		}, false, nil
	}
}

func markHostLockKept(repoDir string, hostID string, runID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	return replaceHostLockOwner(repoDir, hostID, fmt.Sprintf("host: %s\nkeptRun: %s\n", hostID, runID))
}

func markHostLockActive(repoDir string, hostID string, runID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	return replaceHostLockOwner(repoDir, hostID, fmt.Sprintf("host: %s\npid: %d\nactiveRun: %s\n", hostID, os.Getpid(), runID))
}

func replaceHostLockOwner(repoDir string, hostID string, content string) error {
	lockDir := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts")
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	lockPath := filepath.Join(lockDir, hostID+".lock")
	if info, err := os.Lstat(lockPath); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Host Lock %s must not be a symlink", lockPath)
	}
	tempFile, err := os.CreateTemp(lockDir, hostID+".kept.*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.WriteString(content); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, lockPath)
}

func isStaleHostLock(lockPath string) (bool, error) {
	content, err := os.ReadFile(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "pid" {
			if ok && strings.TrimSpace(key) == "keptRun" && strings.TrimSpace(value) != "" {
				return false, nil
			}
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return true, nil
		}
		return !processExists(pid), nil
	}
	return true, nil
}
