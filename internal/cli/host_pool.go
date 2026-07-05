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

var errHostLockReapGuard = errors.New("Host Lock reap guard")

type hostRuntime struct {
	TargetProfile string
	HostID        string
	Config        mayaHostConfig
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
	ID             string    `yaml:"id"`
	Health         string    `yaml:"health"`
	Transport      string    `yaml:"transport"`
	SSH            sshConfig `yaml:"ssh"`
	WorkRoot       string    `yaml:"workRoot"`
	Broker         string    `yaml:"broker"`
	MayaVersions   []string  `yaml:"mayaVersions"`
	VisualEvidence *bool     `yaml:"visualEvidence"`
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
		config.FakeStatus = value.Value
		return nil
	}
	type sshConfigAlias sshConfig
	var decoded sshConfigAlias
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*config = sshConfig(decoded)
	return nil
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
		var sawActiveLocked bool
		var lockErr error
		for _, candidate := range candidates {
			if !isHealthyHost(candidate) {
				continue
			}
			release, locked, err := acquireHostLock(repoDir, candidate.ID)
			if err != nil {
				if errors.Is(err, errHostLockReapGuard) {
					lockErr = err
					sawLocked = true
					continue
				}
				return hostRuntime{}, err
			}
			if locked {
				sawLocked = true
				sawActiveLocked = true
				continue
			}
			return hostRuntime{TargetProfile: options.TargetProfile, HostID: candidate.ID, Config: candidate, release: release}, nil
		}

		if !sawLocked {
			if options.HostPin != "" {
				return hostRuntime{}, fmt.Errorf("pinned Maya Host %q is not healthy in Target Profile %q", options.HostPin, options.TargetProfile)
			}
			return hostRuntime{}, fmt.Errorf("no healthy Maya Host available in Target Profile %q", options.TargetProfile)
		}
		if lockErr != nil && !sawActiveLocked {
			return hostRuntime{}, lockErr
		}
		if options.HostLockWait <= 0 || time.Now().After(deadline) {
			if lockErr != nil {
				return hostRuntime{}, lockErr
			}
			return hostRuntime{}, fmt.Errorf("no healthy unlocked Maya Host available in Target Profile %q", options.TargetProfile)
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

func (host mayaHostConfig) usesRealSSH() bool {
	return strings.EqualFold(strings.TrimSpace(host.Transport), "ssh") || host.SSH.Host != ""
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
	reapLockDir := filepath.Join(lockDir, "reap")
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts", "reap")); err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(reapLockDir, 0o755); err != nil {
		return nil, false, err
	}
	lockPath := filepath.Join(lockDir, hostID+".lock")
	reapLockPath := filepath.Join(reapLockDir, hostID+".lock")
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
		reaping, err := hostLockReapInProgress(reapLockPath)
		if err != nil {
			return nil, false, err
		}
		if reaping {
			return nil, true, nil
		}
		err = os.Link(tempPath, lockPath)
		if errors.Is(err, os.ErrExist) {
			stale, err := isStaleHostLock(lockPath)
			if err != nil {
				return nil, false, err
			}
			if !stale {
				return nil, true, nil
			}
			releaseReap, acquired, err := acquireHostLockReapGuard(reapLockPath, hostID)
			if err != nil {
				return nil, false, err
			}
			if !acquired {
				return nil, true, nil
			}
			stale, err = isStaleHostLock(lockPath)
			if err != nil {
				_ = releaseReap()
				return nil, false, err
			}
			if !stale {
				_ = releaseReap()
				return nil, true, nil
			}
			if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				_ = releaseReap()
				return nil, false, err
			}
			err = os.Link(tempPath, lockPath)
			reapErr := releaseReap()
			if err != nil {
				if reapErr != nil {
					return nil, false, reapErr
				}
				if errors.Is(err, os.ErrExist) {
					continue
				}
				return nil, false, err
			}
			if reapErr != nil {
				_ = os.Remove(lockPath)
				return nil, false, reapErr
			}
			return func() error {
				return os.Remove(lockPath)
			}, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return func() error {
			return os.Remove(lockPath)
		}, false, nil
	}
}

func hostLockReapInProgress(reapLockPath string) (bool, error) {
	info, err := os.Lstat(reapLockPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: %s must not be a symlink", errHostLockReapGuard, reapLockPath)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: %s must be a regular file", errHostLockReapGuard, reapLockPath)
	}
	content, err := os.ReadFile(reapLockPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Host Lock reap guard %s: %w", reapLockPath, err)
	}
	guardContent := string(content)
	for _, line := range strings.Split(guardContent, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "pid" {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return false, fmt.Errorf("%w: %s has invalid pid; remove it after verifying no Host Lock reap is active", errHostLockReapGuard, reapLockPath)
		}
		if !processExists(pid) {
			currentContent, err := os.ReadFile(reapLockPath)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("read Host Lock reap guard %s: %w", reapLockPath, err)
			}
			if string(currentContent) != guardContent {
				return true, nil
			}
			return false, fmt.Errorf("%w: stale guard %s from pid %d; remove it after verifying no Host Lock reap is active", errHostLockReapGuard, reapLockPath, pid)
		}
		return true, nil
	}
	if time.Since(info.ModTime()) > 5*time.Second {
		return false, fmt.Errorf("%w: %s has no pid; remove it after verifying no Host Lock reap is active", errHostLockReapGuard, reapLockPath)
	}
	// Be conservative: an empty guard may be a freshly created guard that has
	// not been populated yet.
	return true, nil
}

func acquireHostLockReapGuard(reapLockPath string, hostID string) (func() error, bool, error) {
	file, err := os.OpenFile(reapLockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := fmt.Fprintf(file, "host: %s\npid: %d\n", hostID, os.Getpid()); err != nil {
		file.Close()
		_ = os.Remove(reapLockPath)
		return nil, false, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(reapLockPath)
		return nil, false, err
	}
	return func() error { return os.Remove(reapLockPath) }, true, nil
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
