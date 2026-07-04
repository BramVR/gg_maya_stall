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
	release       func() error
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
	ID             string   `yaml:"id"`
	Health         string   `yaml:"health"`
	SSH            string   `yaml:"ssh"`
	WorkRoot       string   `yaml:"workRoot"`
	Broker         string   `yaml:"broker"`
	MayaVersions   []string `yaml:"mayaVersions"`
	VisualEvidence *bool    `yaml:"visualEvidence"`
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
			return hostRuntime{TargetProfile: options.TargetProfile, HostID: candidate.ID, release: release}, nil
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
	if err := yaml.Unmarshal(content, &config); err != nil {
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
	content := fmt.Sprintf("host: %s\nkeptRun: %s\n", hostID, runID)
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
