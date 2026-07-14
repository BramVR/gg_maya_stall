package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	gosruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestAcceptedRunWithMissingRepoConfigEmitsRunIDAndMinimalEvidence(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, time.July, 14, 9, 30, 0, 0, time.UTC)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := RunWithRuntime(
		[]string{"run", "smoke"},
		&stdout,
		&stderr,
		dir,
		"test-version",
		runRuntime{Now: func() time.Time { return now }},
	)

	if code != 1 {
		t.Fatalf("accepted config failure exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := "20260714T093000.000000000Z"
	for _, want := range []string{
		"run: " + runID,
		"accepted: true",
		"status: failed",
		"failedLayer: repo-config",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("accepted config failure output missing %q:\n%s", want, stdout.String())
		}
	}
	if !strings.Contains(stderr.String(), "no Maya Stall repo config found") {
		t.Fatalf("accepted config failure stderr = %q", stderr.String())
	}

	evidenceDir := filepath.Join(dir, "artifacts", "maya-stall", runID)
	bundle := readEvidenceBundle(t, evidenceDir)
	if bundle.Version != 1 || bundle.RunID != runID || bundle.Scenario != "smoke" || bundle.Status != resultStatusFailed {
		t.Fatalf("minimal Evidence Bundle identity = %+v", bundle)
	}
	if bundle.Failure == nil || bundle.Failure.FailedLayer != "repo-config" || bundle.Failure.Diagnostic == "" || bundle.Failure.RemediationHint == "" {
		t.Fatalf("minimal Evidence Bundle failure = %+v", bundle.Failure)
	}
	if bundle.Failure.CaptureState != "not-started" || bundle.Failure.CleanupState != "not-needed" {
		t.Fatalf("minimal Evidence Bundle capture/cleanup = %+v", bundle.Failure)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(evidenceDir, evidenceManifestFileName))
	if err != nil {
		t.Fatalf("read minimal manifest: %v", err)
	}
	var manifest runManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse minimal manifest: %v", err)
	}
	if manifest.Version != 1 || manifest.RunID != runID || manifest.Scenario != "smoke" {
		t.Fatalf("minimal manifest = %+v", manifest)
	}
	requireSequencedRunEvents(t, filepath.Join(evidenceDir, evidenceEventsFileName))
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", runID)); err != nil {
		t.Fatalf("accepted failed Run State missing: %v", err)
	}
}

func TestRunSyntaxDistinguishesUsageFromAcceptedSubmission(t *testing.T) {
	t.Run("syntax fails before Scenario identity", func(t *testing.T) {
		dir := t.TempDir()
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := Run([]string{"run", "--unknown", "smoke"}, &stdout, &stderr, dir, "test-version")

		if code != 2 || stdout.Len() != 0 {
			t.Fatalf("pre-identity syntax failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat(filepath.Join(dir, ".maya-stall")); !os.IsNotExist(err) {
			t.Fatalf("pre-identity syntax failure created Run State: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "artifacts")); !os.IsNotExist(err) {
			t.Fatalf("pre-identity syntax failure created evidence: %v", err)
		}
	})

	t.Run("syntax fails after Scenario identity", func(t *testing.T) {
		dir := t.TempDir()
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := Run([]string{"run", "smoke", "--unknown"}, &stdout, &stderr, dir, "test-version")

		if code != 1 {
			t.Fatalf("identified Scenario syntax failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		runID := outputValue(t, stdout.String(), "run")
		bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
		if bundle.Failure == nil || bundle.Failure.FailedLayer != "submission" {
			t.Fatalf("identified Scenario syntax failure = %+v", bundle.Failure)
		}
	})
}

func TestAcceptedPreMayaFailuresProduceClassifiedMinimalEvidence(t *testing.T) {
	tests := []struct {
		name         string
		prepare      func(*testing.T) (string, runRuntime)
		args         []string
		failedLayer  string
		cleanupState string
	}{
		{
			name: "Scenario",
			prepare: func(t *testing.T) (string, runRuntime) {
				return writeRunConfigFixture(t), runRuntime{}
			},
			args:         []string{"run", "missing"},
			failedLayer:  "scenario",
			cleanupState: "not-needed",
		},
		{
			name: "host selection",
			prepare: func(t *testing.T) (string, runRuntime) {
				dir := writeRunConfigFixture(t)
				hostConfig := filepath.Join(dir, "hosts.yaml")
				mustWriteFile(t, hostConfig, `version: 1
targetProfiles:
  default:
    hostPool: default
hostPools:
  default:
    hosts:
      - id: maya-win-01
        health: unhealthy
`)
				return dir, runRuntime{}
			},
			args:         []string{"run", "--host-config", "hosts.yaml", "smoke"},
			failedLayer:  "host-selection",
			cleanupState: "not-needed",
		},
		{
			name: "remote check",
			prepare: func(t *testing.T) (string, runRuntime) {
				return writeRunConfigFixture(t), runRuntime{ReadinessHost: unreachableReadinessHost{}}
			},
			args:         []string{"run", "smoke"},
			failedLayer:  "remote-check",
			cleanupState: "completed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir, runtime := test.prepare(t)
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := RunWithRuntime(test.args, &stdout, &stderr, dir, "test-version", runtime)

			if code != 1 {
				t.Fatalf("accepted %s failure = code %d stdout %q stderr %q", test.name, code, stdout.String(), stderr.String())
			}
			runID := outputValue(t, stdout.String(), "run")
			bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
			if bundle.Failure == nil || bundle.Failure.FailedLayer != test.failedLayer || bundle.Failure.CleanupState != test.cleanupState {
				t.Fatalf("accepted %s failure = %+v", test.name, bundle.Failure)
			}
			if bundle.Failure.CaptureState != "not-started" {
				t.Fatalf("accepted %s capture state = %q", test.name, bundle.Failure.CaptureState)
			}
			if test.failedLayer == "remote-check" {
				lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
				if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
					t.Fatalf("remote-check failure Host Lock = %v, want released", err)
				}
			}
		})
	}
}

func TestRunJSONDistinguishesUsageErrorFromAcceptedFailure(t *testing.T) {
	t.Run("usage error", func(t *testing.T) {
		dir := t.TempDir()
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := Run([]string{"run", "--json"}, &stdout, &stderr, dir, "test-version")

		if code != 2 || stderr.Len() != 0 {
			t.Fatalf("JSON usage error = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		results := decodeRunJSONLines(t, stdout.Bytes())
		if len(results) != 1 || results[0].Kind != "usage-error" || results[0].Accepted || results[0].Error == "" {
			t.Fatalf("JSON usage error = %+v", results)
		}
	})

	t.Run("accepted failure", func(t *testing.T) {
		dir := t.TempDir()
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		code := Run([]string{"run", "smoke", "--unknown", "--json"}, &stdout, &stderr, dir, "test-version")

		if code != 1 {
			t.Fatalf("JSON accepted failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		results := decodeRunJSONLines(t, stdout.Bytes())
		if len(results) != 2 || results[0].Kind != "run-accepted" || !results[0].Accepted || results[0].RunID == "" {
			t.Fatalf("JSON acceptance = %+v", results)
		}
		if results[1].Kind != "run" || !results[1].Accepted || results[1].RunID != results[0].RunID || results[1].Status != resultStatusFailed || results[1].FailedLayer != "submission" {
			t.Fatalf("JSON accepted failure = %+v", results)
		}
	})
}

func TestAcceptedBrokerStartFailureEmitsTerminalJSON(t *testing.T) {
	dir := writeRunConfigFixture(t)
	runtime := defaultRunRuntime()
	runtime.Broker = startFailingSessionBroker{message: "broker unavailable"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := RunWithRuntime([]string{"run", "--json", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)

	if code != 1 {
		t.Fatalf("accepted broker failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	results := decodeRunJSONLines(t, stdout.Bytes())
	if len(results) != 2 {
		t.Fatalf("accepted broker failure JSON records = %+v", results)
	}
	if results[0].Kind != "run-accepted" || !results[0].Accepted || results[0].RunID == "" {
		t.Fatalf("accepted broker failure acceptance = %+v", results)
	}
	if results[1].Kind != "run" || !results[1].Accepted || results[1].RunID != results[0].RunID || results[1].Status != resultStatusFailed {
		t.Fatalf("accepted broker failure terminal record = %+v", results)
	}
}

func TestRunJSONModeSurvivesMissingOptionValue(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"run", "smoke", "--stop-after", "--json"}, &stdout, &stderr, dir, "test-version")

	if code != 1 {
		t.Fatalf("identified JSON submission failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	results := decodeRunJSONLines(t, stdout.Bytes())
	if len(results) != 2 || results[0].Kind != "run-accepted" || results[1].Kind != "run" || results[1].Status != resultStatusFailed {
		t.Fatalf("identified JSON submission records = %+v", results)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"run", "smoke", "--host-config", "--json"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("missing JSON-adjacent option value = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	results = decodeRunJSONLines(t, stdout.Bytes())
	if len(results) != 2 || results[1].FailedLayer != "submission" || !strings.Contains(results[1].Diagnostic, "--host-config needs a path") {
		t.Fatalf("missing JSON-adjacent option classification = %+v", results)
	}
}

func TestUnacceptedOwnedDirectoriesAreCleaned(t *testing.T) {
	dir := t.TempDir()
	run := newFreshRun(dir, runOptions{ScenarioName: "smoke"}, runRuntime{Now: time.Now}).(*freshRunLifecycle)
	if err := run.accept(); err != nil {
		t.Fatalf("accept fixture run: %v", err)
	}
	run.accepted = false

	if err := run.cleanupUnacceptedOwnership(); err != nil {
		t.Fatalf("clean unaccepted ownership: %v", err)
	}
	if _, err := os.Stat(run.context.StateDir); !os.IsNotExist(err) {
		t.Fatalf("unaccepted Run State = %v", err)
	}
	if _, err := os.Stat(run.context.EvidenceDir); !os.IsNotExist(err) {
		t.Fatalf("unaccepted Evidence directory = %v", err)
	}
}

func TestPayloadSnapshotFailureRemainsScenarioLayer(t *testing.T) {
	if gosruntime.GOOS == "windows" {
		t.Skip("POSIX file mode required")
	}
	dir := writeRunConfigFixture(t)
	payloadPath := filepath.Join(dir, "maya", "smoke.py")
	if err := os.Chmod(payloadPath, 0); err != nil {
		t.Fatalf("make payload unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(payloadPath, 0o644) })
	if file, err := os.Open(payloadPath); err == nil {
		_ = file.Close()
		t.Skip("test user can read mode-000 files")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")

	if code != 1 {
		t.Fatalf("payload snapshot failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	runID := outputValue(t, stdout.String(), "run")
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Failure == nil || bundle.Failure.FailedLayer != "scenario" || !strings.Contains(bundle.Failure.RemediationHint, "declared inputs") {
		t.Fatalf("payload snapshot failure classification = %+v", bundle.Failure)
	}
}

func TestAcceptedPostStartFailureWritesTerminalEvidenceBeforeCleanup(t *testing.T) {
	dir := writeRunConfigFixture(t)
	runtime := defaultRunRuntime()
	runtime.Host = stageFailingRunHost{message: "payload staging failed"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)

	if code != 1 {
		t.Fatalf("post-start failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	runID := outputValue(t, stdout.String(), "run")
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Status != resultStatusFailed || bundle.Failure == nil || bundle.Failure.FailedLayer != "execution" || bundle.Failure.CleanupState != "completed" {
		t.Fatalf("post-start terminal evidence = %+v", bundle)
	}
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", runID)); !os.IsNotExist(err) {
		t.Fatalf("post-start failed Run State = %v, want cleaned", err)
	}
}

func TestValidatedHostSelectionReportsLockCleanup(t *testing.T) {
	tests := []struct {
		name             string
		corruptLock      bool
		wantCleanupState string
	}{
		{name: "released", wantCleanupState: "completed"},
		{name: "release failed", corruptLock: true, wantCleanupState: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			hostRoot := t.TempDir()
			hostConfigPath := writeSingleHealthyHostConfigWithWorkRoot(t, dir, hostRoot)
			_, err := selectHostForRunValidated(dir, runOptions{HostConfig: hostConfigPath, TargetProfile: "ci"}, func(mayaHostConfig) error {
				if test.corruptLock {
					mustWriteFile(t, filepath.Join(hostRoot, "state", "locks", "hosts", "alpha.lock"), "changed owner\n")
				}
				return os.ErrInvalid
			})
			var validationErr *hostValidationError
			if !errors.As(err, &validationErr) || validationErr.cleanupState != test.wantCleanupState {
				t.Fatalf("validated host selection error = %#v, want cleanup %q", err, test.wantCleanupState)
			}
		})
	}
}

func TestHostAcquisitionRollbackReportsCleanup(t *testing.T) {
	dir := t.TempDir()
	hostRoot := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall", "state", "locks", "hosts"), "blocks lock directory\n")

	_, _, err := acquireRunHostLock(dir, mayaHostConfig{ID: "alpha", Health: "healthy", WorkRoot: hostRoot})

	var selectionErr *hostValidationError
	if !errors.As(err, &selectionErr) || selectionErr.cleanupState != "completed" {
		t.Fatalf("Host Lock acquisition rollback = %#v", err)
	}
	if _, statErr := os.Stat(filepath.Join(hostRoot, "state", "locks", "hosts", "alpha.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("host-side lock after acquisition rollback = %v", statErr)
	}
}

func TestHostValidationFailureEvidenceKeepsSelectedHost(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostRoot := t.TempDir()
	hostConfigPath := filepath.Join(dir, "hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        health: healthy
        workRoot: "`+filepath.ToSlash(hostRoot)+`"
        trustedPluginArtifactsRoot: relative/path
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")

	if code != 1 {
		t.Fatalf("host validation failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	runID := outputValue(t, stdout.String(), "run")
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Host != "alpha" || bundle.TargetProfile != "ci" || bundle.Runtime.Profile != "fake-local" || bundle.Failure == nil || bundle.Failure.CleanupState != "completed" {
		t.Fatalf("host validation failure provenance = %+v", bundle)
	}
}

func TestMinimalEvidenceFallbackReplacesPartialMetadata(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	evidenceDir := filepath.Join(dir, "evidence")
	context := runContext{
		StateDir:    stateDir,
		EvidenceDir: evidenceDir,
		EventsPath:  filepath.Join(stateDir, evidenceEventsFileName),
		LogPath:     filepath.Join(stateDir, "session.log"),
	}
	manifest := runManifest{Version: evidenceSchemaVersion, RunID: "run-1", Scenario: "smoke"}
	mustWriteFile(t, filepath.Join(stateDir, evidenceManifestFileName), `{"version":1,"runId":"run-1","scenario":"smoke"}`+"\n")
	mustWriteFile(t, context.EventsPath, `{"event":"run.accepted","detail":"smoke"}`+"\n")
	mustWriteFile(t, context.LogPath, "terminal failure\n")
	mustWriteFile(t, filepath.Join(evidenceDir, evidenceManifestFileName), "partial manifest\n")
	mustWriteFile(t, filepath.Join(evidenceDir, evidenceLogPath), "partial log\n")
	failure := &runFailureEvidence{FailedLayer: "execution", Diagnostic: "failed", RemediationHint: "retry", CaptureState: "not-captured", CleanupState: "pending"}

	if err := writeMinimalEvidenceBundle(context, manifest, ScenarioResult{Status: resultStatusFailed}, failure); err != nil {
		t.Fatalf("replace partial evidence metadata: %v", err)
	}
	bundle := readEvidenceBundle(t, evidenceDir)
	if bundle.Failure == nil || bundle.Failure.FailedLayer != "execution" {
		t.Fatalf("fallback Evidence Bundle = %+v", bundle)
	}
	logContent, err := os.ReadFile(filepath.Join(evidenceDir, evidenceLogPath))
	if err != nil || string(logContent) != "terminal failure\n" {
		t.Fatalf("fallback log = %q err %v", string(logContent), err)
	}
}

func TestCopySequencedEventsIgnoresBlankLines(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "state-events.jsonl")
	destination := filepath.Join(dir, "evidence-events.jsonl")
	mustWriteFile(t, source, "{\"event\":\"first\"}\n\n{\"event\":\"second\"}\n")

	if err := copySequencedEvents(source, destination); err != nil {
		t.Fatalf("copy sequenced events: %v", err)
	}
	requireSequencedRunEvents(t, destination)
}

func TestTerminalEvidencePreservesPartialVisualCapture(t *testing.T) {
	dir := writeRunConfigFixture(t)
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read run config: %v", err)
	}
	updated := strings.Replace(string(content), "screenshots:\n        enabled: false", "screenshots:\n        enabled: true\n      recording:\n        enabled: true", 1)
	mustWriteFile(t, configPath, updated)
	runtime := defaultRunRuntime()
	runtime.Broker = partialVisualEvidenceBroker{fakeSessionBroker: fakeSessionBroker{Result: ScenarioResult{Status: resultStatusPassed, Summary: "passed"}}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)

	if code != 1 {
		t.Fatalf("partial Visual Evidence run = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	runID := outputValue(t, stdout.String(), "run")
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Failure == nil || bundle.Failure.CaptureState != "partial" || len(bundle.VisualEvidence) != 1 {
		t.Fatalf("partial capture Evidence Bundle = %+v", bundle)
	}
	artifact := bundle.VisualEvidence[0]
	if artifact.Kind != "screenshot" || artifact.Origin != visualEvidenceOriginFakeBrokerCapture || artifact.SHA256 == "" {
		t.Fatalf("partial capture provenance = %+v", artifact)
	}
	found := false
	for _, cataloged := range bundle.Artifacts {
		if cataloged.Path == artifact.Path {
			found = true
		}
	}
	if !found {
		t.Fatalf("partial capture missing from artifact catalog: %+v", bundle.Artifacts)
	}
}

func TestBrokerFailureScreenshotIsPartialWhenRecordingWasRequested(t *testing.T) {
	dir := writeRunConfigFixture(t)
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read run config: %v", err)
	}
	updated := strings.Replace(string(content), "screenshots:\n        enabled: false", "screenshots:\n        enabled: true\n      recording:\n        enabled: true", 1)
	mustWriteFile(t, configPath, updated)
	runtime := defaultRunRuntime()
	runtime.Broker = failingScreenshotSessionBroker{message: "broker failed before result"}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)

	if code != 1 {
		t.Fatalf("broker failure = code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	runID := outputValue(t, stdout.String(), "run")
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Failure == nil || bundle.Failure.CaptureState != "partial" || len(bundle.VisualEvidence) != 1 {
		t.Fatalf("broker failure partial capture = %+v", bundle)
	}
}

func TestEarlyFailureStopsHeartbeatBeforeHostLockRelease(t *testing.T) {
	dir := t.TempDir()
	run := newFreshRun(dir, runOptions{ScenarioName: "smoke"}, runRuntime{Now: time.Now}).(*freshRunLifecycle)
	if err := run.accept(); err != nil {
		t.Fatalf("accept run: %v", err)
	}
	run.failedLayer = failureLayerRemoteCheck
	stopped := false
	run.stopHostLockHeartbeat = func() error {
		stopped = true
		return nil
	}
	run.checkHostLockHeartbeat = func() error { return nil }
	run.host = hostRuntime{HostID: "alpha", release: func() error {
		if !stopped {
			return errors.New("released before heartbeat stopped")
		}
		return nil
	}}
	run.releaseHostLock = true

	outcome, err := run.finishEarlyFailure(errors.New("remote check failed"))

	if err == nil || !strings.Contains(err.Error(), "remote check failed") {
		t.Fatalf("early failure error = %v", err)
	}
	if !stopped || run.stopHostLockHeartbeat != nil || outcome.Failure == nil || outcome.Failure.CleanupState != "completed" {
		t.Fatalf("early failure cleanup = stopped %t outcome %+v", stopped, outcome)
	}
}

type partialVisualEvidenceBroker struct {
	fakeSessionBroker
}

func (broker partialVisualEvidenceBroker) CaptureRecording(runContext, recordingRequest) (visualEvidenceArtifact, error) {
	return visualEvidenceArtifact{}, errors.New("recording capture failed")
}

func decodeRunJSONLines(t *testing.T, content []byte) []runCommandJSON {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(content))
	var results []runCommandJSON
	for decoder.More() {
		var result runCommandJSON
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("decode run JSON: %v", err)
		}
		results = append(results, result)
	}
	return results
}

type unreachableReadinessHost struct{}

func (unreachableReadinessHost) StagePayload(runContext, []manifestPayload) error { return nil }
func (unreachableReadinessHost) ValidateTransportConfig() error                   { return nil }
func (unreachableReadinessHost) ProbeTransport(time.Duration) error {
	return os.ErrDeadlineExceeded
}

func requireSequencedRunEvents(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ordered events: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(content), []byte{'\n'})
	if len(lines) < 2 {
		t.Fatalf("ordered event count = %d, want at least 2", len(lines))
	}
	for index, line := range lines {
		var event struct {
			Sequence int `json:"sequence"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("parse ordered event %d: %v", index+1, err)
		}
		if event.Sequence != index+1 {
			t.Fatalf("event %d sequence = %d, want %d", index+1, event.Sequence, index+1)
		}
	}
}
