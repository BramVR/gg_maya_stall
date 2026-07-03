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
