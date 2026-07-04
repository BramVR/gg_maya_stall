package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHelpAndVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr, "", "test-version")
	if code != 0 {
		t.Fatalf("help exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "maya-stall") || !strings.Contains(stdout.String(), "init") {
		t.Fatalf("help output missing command surface:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"version"}, &stdout, &stderr, "", "test-version")
	if code != 0 {
		t.Fatalf("version exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "maya-stall test-version" {
		t.Fatalf("version output = %q", got)
	}
}

func TestInitWritesRepoOnlySmokeScenario(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"init"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0; stderr: %s", code, stderr.String())
	}

	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	content := string(contentBytes)
	for _, want := range []string{
		"version: 1",
		"scenarios:",
		"smoke:",
		"mayaVersion:",
		"payload:",
		"evidence:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{
		"host",
		"Host",
		"hostname",
		"Host Pool",
		"Host Credentials",
		"ssh",
		"credential",
		"password",
		"private",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("generated config contains forbidden host/credential detail %q:\n%s", forbidden, content)
		}
	}
}

func TestInitConfigRunsFakeSmokeScenario(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"init"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: passed") {
		t.Fatalf("run output missing passed status:\n%s", stdout.String())
	}
}

func TestDiscoverConfigRecognizesSupportedRepoFilenames(t *testing.T) {
	for _, name := range []string{".maya-stall.yaml", "maya-stall.yaml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("version: 1\nscenarios: {}\n"), 0o644); err != nil {
				t.Fatalf("write config fixture: %v", err)
			}

			got, err := DiscoverConfig(dir)
			if err != nil {
				t.Fatalf("DiscoverConfig returned error: %v", err)
			}
			if got != path {
				t.Fatalf("DiscoverConfig = %q, want %q", got, path)
			}
		})
	}
}

func TestRunScenarioStagesPayloadAndWritesStateAndEvidence(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: "passed", Summary: "fake broker result"}}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: passed") {
		t.Fatalf("run output missing passed status:\n%s", stdout.String())
	}

	runState := onlyRunDir(t, filepath.Join(dir, ".maya-stall", "state", "runs"))
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))

	manifestBytes, err := os.ReadFile(filepath.Join(runState, "manifest.json"))
	if err != nil {
		t.Fatalf("read run manifest: %v", err)
	}
	var manifest struct {
		Scenario string `json:"scenario"`
		Payload  []struct {
			Kind   string `json:"kind"`
			Source string `json:"source"`
			Staged string `json:"staged"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse run manifest: %v", err)
	}
	if manifest.Scenario != "smoke" {
		t.Fatalf("manifest scenario = %q, want smoke", manifest.Scenario)
	}
	wantPayload := map[string]string{
		"scripts:maya/smoke.py":          filepath.Join("payload", "scripts", "maya", "smoke.py"),
		"scenes:scenes/start.ma":         filepath.Join("payload", "scenes", "scenes", "start.ma"),
		"pluginArtifacts:build/demo.mll": filepath.Join("payload", "pluginArtifacts", "build", "demo.mll"),
	}
	for _, item := range manifest.Payload {
		key := item.Kind + ":" + item.Source
		want, ok := wantPayload[key]
		if !ok {
			t.Fatalf("unexpected manifest payload item: %+v", item)
		}
		if item.Staged != want {
			t.Fatalf("manifest staged path for %s = %q, want %q", key, item.Staged, want)
		}
		if _, err := os.Stat(filepath.Join(runState, item.Staged)); err != nil {
			t.Fatalf("staged payload %s: %v", item.Staged, err)
		}
		delete(wantPayload, key)
	}
	if len(wantPayload) != 0 {
		t.Fatalf("manifest missing payload items: %#v", wantPayload)
	}

	for _, path := range []string{
		filepath.Join(runState, "events.jsonl"),
		filepath.Join(runState, "logs", "session.log"),
		filepath.Join(runState, "workspace", "outputs", "smoke-result.json"),
		filepath.Join(evidence, "evidence.json"),
		filepath.Join(evidence, "events.jsonl"),
		filepath.Join(evidence, "logs", "session.log"),
		filepath.Join(evidence, "manifest.json"),
		filepath.Join(evidence, "scenario-result.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected run artifact %s: %v", path, err)
		}
	}
}

func TestDoctorReportsHealthyLocalConfigAndTargetProfile(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"local-config: ok - .maya-stall.yaml",
		"target-profile: ok - default",
		"host-pool: ok - default",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorLocalConfigFailureIncludesRepairHint(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("doctor exit code = %d, want 1; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"local-config: fail",
		"no Maya Stall repo config found",
		"hint: Run maya-stall init or fix the repo config schema.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorReportsHostSpecificHealthLayers(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeLayeredHostConfig(t, dir)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "alpha"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"host: ok - alpha",
		"fake-ssh: ok - reachable",
		"work-root: ok - writable",
		"session-broker: ok - reachable",
		"maya-version: ok - 2025",
		"visual-evidence: ok - available",
		"host-lock: ok - unlocked",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorScenarioValidatesInputsAndMayaVersion(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeLayeredHostConfig(t, dir)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci", "--scenario", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{
		"scenario-inputs: ok - smoke",
		"host: ok - alpha",
		"maya-version: ok - 2025 satisfies Scenario smoke",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorFailureLayersIncludeRepairHints(t *testing.T) {
	tests := []struct {
		name       string
		config     string
		args       []string
		lockHost   string
		wantLayer  string
		wantDetail string
		wantHint   string
	}{
		{
			name: "target profile",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
`,
			args:       []string{"--target-profile", "missing"},
			wantLayer:  "target-profile: fail",
			wantDetail: `unknown Target Profile "missing"`,
			wantHint:   "Choose a configured Target Profile",
		},
		{
			name: "host pool",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts: []
`,
			wantLayer:  "host-pool: fail",
			wantDetail: "has no Maya Hosts",
			wantHint:   "Add at least one Maya Host",
		},
		{
			name: "fake ssh",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh: down
`,
			wantLayer:  "fake-ssh: fail",
			wantDetail: "down",
			wantHint:   "Fix SSH reachability",
		},
		{
			name: "work root",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        workRoot: unwritable
`,
			wantLayer:  "work-root: fail",
			wantDetail: "unwritable",
			wantHint:   "Fix the host work root",
		},
		{
			name: "session broker",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        broker: down
`,
			wantLayer:  "session-broker: fail",
			wantDetail: "down",
			wantHint:   "Start or repair the Session Broker",
		},
		{
			name: "maya version",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        mayaVersions: ["2024"]
`,
			args:       []string{"--scenario", "smoke"},
			wantLayer:  "maya-version: fail",
			wantDetail: "needs 2025",
			wantHint:   "Install a compatible Autodesk Maya version",
		},
		{
			name: "visual evidence",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        visualEvidence: false
`,
			wantLayer:  "visual-evidence: fail",
			wantDetail: "unavailable",
			wantHint:   "Enable screenshot or recording capture",
		},
		{
			name: "host lock",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
`,
			lockHost:   "alpha",
			wantLayer:  "host-lock: fail",
			wantDetail: "locked",
			wantHint:   "Wait for the active Fresh Run",
		},
		{
			name: "scenario inputs",
			config: `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
`,
			args:       []string{"--scenario", "smoke"},
			wantLayer:  "scenario-inputs: fail",
			wantDetail: "missing.py",
			wantHint:   "Fix the Scenario payload paths",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			if tt.name == "scenario inputs" {
				configPath := filepath.Join(dir, ".maya-stall.yaml")
				contentBytes, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatalf("read config fixture: %v", err)
				}
				content := strings.Replace(string(contentBytes), "maya/smoke.py", "maya/missing.py", 1)
				if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
					t.Fatalf("write missing payload config fixture: %v", err)
				}
			}
			hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
			mustWriteFile(t, hostConfigPath, tt.config)
			if tt.lockHost != "" {
				release, locked, err := acquireHostLock(dir, tt.lockHost)
				if err != nil {
					t.Fatalf("pre-lock host: %v", err)
				}
				if locked {
					t.Fatal("host was already locked")
				}
				defer release()
			}
			args := []string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "alpha"}
			args = append(args, tt.args...)
			var stdout, stderr bytes.Buffer

			code := Run(args, &stdout, &stderr, dir, "test-version")
			if code != 1 {
				t.Fatalf("doctor exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
			}
			for _, want := range []string{tt.wantLayer, tt.wantDetail, "hint: " + tt.wantHint} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
				}
			}
		})
	}
}

func TestRunScenarioSelectsFirstHealthyUnlockedHostFromExternalConfig(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: unhealthy
      - id: beta
        health: healthy
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: beta") {
		t.Fatalf("run output missing selected host:\n%s", stdout.String())
	}

	runState := onlyRunDir(t, filepath.Join(dir, ".maya-stall", "state", "runs"))
	manifestBytes, err := os.ReadFile(filepath.Join(runState, "manifest.json"))
	if err != nil {
		t.Fatalf("read run manifest: %v", err)
	}
	var manifest struct {
		TargetProfile string `json:"targetProfile"`
		Host          string `json:"host"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse run manifest: %v", err)
	}
	if manifest.TargetProfile != "ci" || manifest.Host != "beta" {
		t.Fatalf("manifest selected target/host = %q/%q, want ci/beta", manifest.TargetProfile, manifest.Host)
	}
}

func TestRunScenarioSkipsLockedHostForNextHealthyHost(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
      - id: beta
        health: healthy
`)
	release, locked, err := acquireHostLock(dir, "alpha")
	if err != nil {
		t.Fatalf("pre-lock alpha: %v", err)
	}
	if locked {
		t.Fatal("alpha was already locked")
	}
	defer release()
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: beta") {
		t.Fatalf("run output missing next unlocked host:\n%s", stdout.String())
	}
}

func TestRunScenarioRemovesStaleHostLock(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "alpha.lock")
	mustWriteFile(t, lockPath, "host: alpha\npid: -1\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-fail-fast", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: alpha") {
		t.Fatalf("run output missing host after stale lock removal:\n%s", stdout.String())
	}
}

func TestRunScenarioRemovesMalformedHostLock(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "alpha.lock")
	mustWriteFile(t, lockPath, "host: alpha\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-fail-fast", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: alpha") {
		t.Fatalf("run output missing host after malformed lock removal:\n%s", stdout.String())
	}
}

func TestRunScenarioHostPinSelectsRequestedHostOrFailsClearly(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
      - id: beta
        health: healthy
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "beta", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("pinned run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: beta") {
		t.Fatalf("pinned run output missing requested host:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "gamma", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 2 {
		t.Fatalf("missing pinned host exit code = %d, want 2; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), `pinned Maya Host "gamma" is not in Target Profile "ci"`) {
		t.Fatalf("missing pinned host error = %q", stderr.String())
	}
}

func TestRunScenarioHostLockPreventsConcurrentFailFastRun(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	firstBroker := newBlockingBroker(ScenarioResult{Status: "passed", Summary: "first done"})
	firstDone := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		runtime := defaultRunRuntime()
		runtime.Broker = firstBroker
		firstDone <- RunWithRuntime([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	}()
	<-firstBroker.started
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "alpha.lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("active run did not hold host lock: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-fail-fast", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("concurrent fail-fast run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `no healthy unlocked Maya Host available in Target Profile "ci"`) {
		t.Fatalf("concurrent lock error = %q", stderr.String())
	}

	close(firstBroker.release)
	select {
	case code := <-firstDone:
		if code != 0 {
			t.Fatalf("first run exit code = %d, want 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not finish after release")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("host lock was not released: %v", err)
	}
}

func TestRunScenarioHostLockWaitsForRelease(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	firstBroker := newBlockingBroker(ScenarioResult{Status: "passed", Summary: "first done"})
	firstDone := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		runtime := defaultRunRuntime()
		runtime.Broker = firstBroker
		firstDone <- RunWithRuntime([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	}()
	<-firstBroker.started

	secondDone := make(chan runResult, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-wait", "2s", "smoke"}, &stdout, &stderr, dir, "test-version")
		secondDone <- runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}()
	select {
	case result := <-secondDone:
		t.Fatalf("waiting run finished before lock release: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	close(firstBroker.release)
	select {
	case code := <-firstDone:
		if code != 0 {
			t.Fatalf("first run exit code = %d, want 0", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not finish after release")
	}
	select {
	case result := <-secondDone:
		if result.code != 0 {
			t.Fatalf("waiting run exit code = %d, want 0; stdout: %s stderr: %s", result.code, result.stdout, result.stderr)
		}
		if !strings.Contains(result.stdout, "host: alpha") {
			t.Fatalf("waiting run output missing host:\n%s", result.stdout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiting run did not finish after lock release")
	}
}

func TestRunScenarioReportsHostLockReleaseFailure(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = lockCorruptingBroker{}

	code := RunWithRuntime([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "release Host Lock for alpha") {
		t.Fatalf("release failure error = %q", stderr.String())
	}
}

func TestRunScenarioResultFailureDrivesExitCode(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: "failed", Summary: "fake broker result"}}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: failed") {
		t.Fatalf("run output missing failed status:\n%s", stdout.String())
	}
}

func TestRunScenarioRequiresKnownScenario(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "missing"}, &stdout, &stderr, dir, "test-version")
	if code != 2 {
		t.Fatalf("run exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown Scenario") {
		t.Fatalf("missing scenario error = %q", stderr.String())
	}
}

func TestRunScenarioValidatesScenarioResultBeforeRunState(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      scripts: []
    expectedOutputs:
      scenarioResult: "../result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "repo-relative") {
		t.Fatalf("scenario result validation error = %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall")); !os.IsNotExist(err) {
		t.Fatalf("run state directory exists after invalid scenario result: %v", err)
	}
}

func TestRunScenarioRejectsReservedPayloadPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall", "state", "previous.txt"), "state should not stage\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      scripts:
        - ".maya-stall"
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "reserved for Maya Stall run state") {
		t.Fatalf("reserved path error = %q", stderr.String())
	}
}

func TestPayloadPathValidationRejectsReservedPathCaseVariants(t *testing.T) {
	for _, path := range []string{".MAYA-STALL", ".Maya-Stall/state", "artifacts", "Artifacts/Maya-Stall", "ARTIFACTS/MAYA-STALL/run"} {
		t.Run(path, func(t *testing.T) {
			_, err := cleanRepoRelativePath(path)
			if err == nil {
				t.Fatalf("cleanRepoRelativePath(%q) returned nil error", path)
			}
			if !strings.Contains(err.Error(), "reserved for Maya Stall run state") {
				t.Fatalf("reserved path error = %q", err.Error())
			}
		})
	}
}

func TestRunScenarioRejectsSymlinkedOutputParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".maya-stall")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      scripts: []
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "must not be a symlink") {
		t.Fatalf("symlinked output parent error = %q", stderr.String())
	}
}

func TestRunScenarioRejectsSymlinkPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := writeRunConfigFixture(t)
	outside := filepath.Join(t.TempDir(), "outside.py")
	mustWriteFile(t, outside, "print('outside')\n")
	linkPath := filepath.Join(dir, "maya", "leaks.py")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	content := strings.Replace(string(contentBytes), "maya/smoke.py", "maya/leaks.py", 1)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write symlink config fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Fatalf("symlink path error = %q", stderr.String())
	}
}

func TestRunScenarioRejectsSymlinkPayloadAncestor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "leaks.py"), "print('outside')\n")
	if err := os.Symlink(outside, filepath.Join(dir, "maya")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      scripts:
        - "maya/leaks.py"
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Fatalf("symlink ancestor error = %q", stderr.String())
	}
}

func TestRunScenarioRejectsSpecialPayloadFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO fixtures are not available on Windows")
	}
	dir := writeRunConfigFixture(t)
	fifoPath := filepath.Join(dir, "maya", "fifo.py")
	if err := exec.Command("mkfifo", fifoPath).Run(); err != nil {
		t.Skipf("create FIFO fixture: %v", err)
	}
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	content := strings.Replace(string(contentBytes), "maya/smoke.py", "maya/fifo.py", 1)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write FIFO config fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "regular file") {
		t.Fatalf("special payload error = %q", stderr.String())
	}
}

func writeRunConfigFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('smoke')\n")
	mustWriteFile(t, filepath.Join(dir, "scenes", "start.ma"), "// fake maya scene\n")
	mustWriteFile(t, filepath.Join(dir, "build", "demo.mll"), "fake plugin artifact\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    description: "Fake smoke Scenario."
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/smoke.py"
      scenes:
        - "scenes/start.ma"
      pluginArtifacts:
        - "build/demo.mll"
    expectedOutputs:
      files: []
      scenarioResult: "outputs/smoke-result.json"
    evidence:
      screenshots:
        enabled: true
      recording:
        enabled: false
`)
	return dir
}

func writeSingleHealthyHostConfig(t *testing.T, dir string) string {
	t.Helper()
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
`)
	return hostConfigPath
}

func writeLayeredHostConfig(t *testing.T, dir string) string {
	t.Helper()
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        ssh: ok
        workRoot: writable
        broker: ok
        mayaVersions: ["2025"]
        visualEvidence: true
`)
	return hostConfigPath
}

type blockingBroker struct {
	started chan struct{}
	release chan struct{}
	result  ScenarioResult
}

type runResult struct {
	code   int
	stdout string
	stderr string
}

func newBlockingBroker(result ScenarioResult) *blockingBroker {
	return &blockingBroker{
		started: make(chan struct{}),
		release: make(chan struct{}),
		result:  result,
	}
}

func (broker *blockingBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	close(broker.started)
	<-broker.release
	if err := os.WriteFile(context.LogPath, []byte("blocking fake Session Broker completed\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", broker.result.Status); err != nil {
		return ScenarioResult{}, err
	}
	return broker.result, nil
}

type lockCorruptingBroker struct{}

func (lockCorruptingBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	lockPath := filepath.Join(context.RepoDir, ".maya-stall", "state", "locks", "hosts", "alpha.lock")
	if err := os.Remove(lockPath); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(filepath.Join(lockPath, "child"), []byte("blocks remove\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("corrupted lock path\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: "passed", Summary: "lock release should fail"}, nil
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}

func onlyRunDir(t *testing.T, parent string) string {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read run dir parent %s: %v", parent, err)
	}
	if len(entries) != 1 {
		t.Fatalf("run dir count in %s = %d, want 1", parent, len(entries))
	}
	if !entries[0].IsDir() {
		t.Fatalf("run entry %s is not a directory", entries[0].Name())
	}
	return filepath.Join(parent, entries[0].Name())
}
