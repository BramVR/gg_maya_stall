package cli

import (
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
		sshCheck := realSSHLayer(host)
		add(sshCheck)
		if sshCheck.Status == "ok" {
			add(realWorkRootLayer(host))
		} else {
			add(failedCheck("work-root", "skipped", "Fix SSH reachability or SSH settings before checking the host work root. See docs/setup/windows-maya-host.md#work-root."))
		}
	} else {
		add(statusLayer("fake-ssh", host.SSH.FakeStatus, "reachable", []string{"", "ok", "healthy", "reachable"}, "Fix SSH reachability for this Maya Host. See docs/setup/windows-maya-host.md#openssh-reachability."))
		add(statusLayer("work-root", host.WorkRoot, "writable", []string{"", "ok", "writable"}, "Fix the host work root path or permissions. See docs/setup/windows-maya-host.md#work-root."))
	}
	add(statusLayer("session-broker", host.Broker, "reachable", []string{"", "ok", "healthy", "reachable"}, "Start or repair the Session Broker on this Maya Host. See docs/setup/windows-maya-host.md#session-broker."))
	add(mayaVersionLayer(options, host, scenario))
	if host.VisualEvidence != nil && !*host.VisualEvidence {
		add(failedCheck("visual-evidence", "unavailable", "Enable screenshot or recording capture through the Session Broker. See docs/setup/windows-maya-host.md#visual-evidence."))
	} else {
		add(okCheck("visual-evidence", "available"))
	}
	add(hostLockLayer(repoDir, host.ID))
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
	reapLockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", "reap", hostID+".lock")
	reaping, err := hostLockReapInProgress(reapLockPath)
	if err != nil {
		return failedCheck("host-lock", err.Error(), "Inspect or remove the Host Lock reap guard after verifying no reap is active. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
	}
	if reaping {
		return failedCheck("host-lock", fmt.Sprintf("%s lock reap in progress", hostID), "Wait for the Host Lock reap to finish. See docs/setup/windows-maya-host.md#host-lock-and-retention.")
	}
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", hostID+".lock")
	_, err = os.Stat(lockPath)
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
