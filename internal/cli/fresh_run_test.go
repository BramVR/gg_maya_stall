package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFreshRunLifecycleOrdersSetupExecuteAndSettle(t *testing.T) {
	dir := writeRunConfigFixture(t)
	recorder := &freshRunLifecycleRecorder{}
	runtime := defaultRunRuntime()
	runtime.Host = recordingRunHost{recorder: recorder}
	runtime.Broker = recordingSessionBroker{recorder: recorder, result: ScenarioResult{Status: resultStatusPassed, Summary: "recorded"}}
	runtime.Now = func() time.Time {
		return time.Date(2026, 7, 6, 12, 30, 0, 0, time.UTC)
	}

	outcome, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, runtime).Run()
	if err != nil {
		t.Fatalf("Fresh Run returned error: %v", err)
	}
	if outcome.Result.Status != resultStatusPassed {
		t.Fatalf("Fresh Run status = %q, want passed", outcome.Result.Status)
	}

	recorder.requireOrder(t,
		"setup.stage-payload",
		"execute.run-scenario",
		"settle.collect-artifacts",
	)
	readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", outcome.RunID))
	if _, _, err := readScenarioResultDocument(filepath.Join(dir, "artifacts", "maya-stall", outcome.RunID, "scenario-result.json")); err != nil {
		t.Fatalf("Fresh Run did not write Scenario Result during settle: %v", err)
	}
}

func TestFreshRunLifecycleSettlesStopPolicyAndFailures(t *testing.T) {
	t.Run("success cleans state and releases Host Lock", func(t *testing.T) {
		dir := writeRunConfigFixture(t)

		outcome, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, defaultRunRuntime()).Run()
		if err != nil {
			t.Fatalf("Fresh Run returned error: %v", err)
		}
		if outcome.StopPolicy != "stopped" || outcome.Result.Status != resultStatusPassed {
			t.Fatalf("Fresh Run outcome = %+v, want stopped passed", outcome)
		}
		if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", outcome.RunID)); !os.IsNotExist(err) {
			t.Fatalf("stopped Fresh Run state = %v, want missing", err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")); !os.IsNotExist(err) {
			t.Fatalf("stopped Fresh Run Host Lock = %v, want missing", err)
		}
		readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", outcome.RunID))
	})

	t.Run("broker error releases Host Lock", func(t *testing.T) {
		dir := writeRunConfigFixture(t)
		runtime := defaultRunRuntime()
		runtime.Broker = failingSessionBroker{message: "broker unavailable"}

		_, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, runtime).Run()
		if err == nil || !strings.Contains(err.Error(), "broker unavailable") {
			t.Fatalf("Fresh Run error = %v, want broker unavailable", err)
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")); !os.IsNotExist(statErr) {
			t.Fatalf("broker-error Host Lock = %v, want missing", statErr)
		}
	})

	t.Run("broker error captures failure desktop evidence", func(t *testing.T) {
		dir := writeFailureScreenshotRunConfigFixture(t)
		runtime := defaultRunRuntime()
		runtime.Broker = failingScreenshotSessionBroker{message: "script.execute failed before Scenario Result"}

		_, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, runtime).Run()
		if err == nil || !strings.Contains(err.Error(), "script.execute failed before Scenario Result") {
			t.Fatalf("Fresh Run error = %v, want script.execute failure", err)
		}
		evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
		bundle := readEvidenceBundle(t, evidence)
		if bundle.Status != resultStatusFailed {
			t.Fatalf("failure Evidence Bundle status = %q, want failed", bundle.Status)
		}
		if len(bundle.VisualEvidence) != 1 {
			t.Fatalf("failure Visual Evidence = %+v, want one desktop screenshot", bundle.VisualEvidence)
		}
		artifact := bundle.VisualEvidence[0]
		if artifact.Kind != "screenshot" || artifact.MediaType != "image/png" || artifact.Path != "screenshots/failure-desktop.png" {
			t.Fatalf("failure screenshot artifact = %+v", artifact)
		}
		if _, statErr := os.Stat(filepath.Join(evidence, artifact.Path)); statErr != nil {
			t.Fatalf("failure screenshot missing: %v", statErr)
		}
		if artifact.TargetProfile != "default" || artifact.Host != defaultFakeHostID {
			t.Fatalf("failure screenshot target metadata = %+v", artifact)
		}
		scenarioResult, found, readErr := readScenarioResultDocument(filepath.Join(evidence, "scenario-result.json"))
		if readErr != nil || !found {
			t.Fatalf("failure Scenario Result = found %v err %v", found, readErr)
		}
		if scenarioResult.Result.Status != resultStatusFailed || !strings.Contains(scenarioResult.Result.Summary, "script.execute failed before Scenario Result") {
			t.Fatalf("failure Scenario Result = %+v", scenarioResult.Result)
		}
	})

	t.Run("broker error honors screenshot opt out", func(t *testing.T) {
		dir := writeRunConfigFixture(t)
		runtime := defaultRunRuntime()
		runtime.Broker = failingScreenshotSessionBroker{message: "script.execute failed before Scenario Result"}

		_, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, runtime).Run()
		if err == nil || !strings.Contains(err.Error(), "script.execute failed before Scenario Result") {
			t.Fatalf("Fresh Run error = %v, want script.execute failure", err)
		}
		evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
		bundle := readEvidenceBundle(t, evidence)
		if len(bundle.VisualEvidence) != 0 {
			t.Fatalf("failure Visual Evidence = %+v, want screenshot opt-out honored", bundle.VisualEvidence)
		}
		if _, statErr := os.Stat(filepath.Join(evidence, "screenshots", "failure-desktop.png")); !os.IsNotExist(statErr) {
			t.Fatalf("failure screenshot opt-out stat = %v, want missing", statErr)
		}
	})

	t.Run("validator failure drives failed status", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: scenarioResultStatus
        status: failed
`)
		outcome, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterAlways}, defaultRunRuntime()).Run()
		if err != nil {
			t.Fatalf("Fresh Run returned error: %v", err)
		}
		if outcome.Result.Status != resultStatusFailed {
			t.Fatalf("Fresh Run status = %q, want failed", outcome.Result.Status)
		}
		bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", outcome.RunID))
		if bundle.Status != resultStatusFailed || len(bundle.Validators) != 1 || bundle.Validators[0].Status != resultStatusFailed {
			t.Fatalf("Fresh Run evidence validators = %+v status %q", bundle.Validators, bundle.Status)
		}
	})

	t.Run("keep on failure retains Host Lock and run state", func(t *testing.T) {
		dir := writeRunConfigFixture(t)
		runtime := defaultRunRuntime()
		runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: resultStatusFailed, Summary: "fake failure"}}

		outcome, err := newFreshRun(dir, runOptions{ScenarioName: "smoke", TargetProfile: "default", StopAfter: stopAfterSuccess}, runtime).Run()
		if err != nil {
			t.Fatalf("Fresh Run returned error: %v", err)
		}
		if outcome.StopPolicy != "kept" || len(outcome.FollowUpCommands) != 3 {
			t.Fatalf("Fresh Run Stop Policy = %q follow-ups %#v, want kept with follow-ups", outcome.StopPolicy, outcome.FollowUpCommands)
		}
		if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", outcome.RunID)); err != nil {
			t.Fatalf("kept Fresh Run state missing: %v", err)
		}
		lockBytes, err := os.ReadFile(filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock"))
		if err != nil {
			t.Fatalf("kept Fresh Run Host Lock missing: %v", err)
		}
		if !strings.Contains(string(lockBytes), "keptRun: "+outcome.RunID) {
			t.Fatalf("kept Fresh Run Host Lock content = %q", string(lockBytes))
		}
	})
}

type freshRunLifecycleRecorder struct {
	events []string
}

func (recorder *freshRunLifecycleRecorder) add(event string) {
	recorder.events = append(recorder.events, event)
}

func (recorder *freshRunLifecycleRecorder) requireOrder(t *testing.T, want ...string) {
	t.Helper()
	if len(recorder.events) != len(want) {
		t.Fatalf("lifecycle events = %#v, want %#v", recorder.events, want)
	}
	for index, event := range want {
		if recorder.events[index] != event {
			t.Fatalf("lifecycle events = %#v, want %#v", recorder.events, want)
		}
	}
}

type recordingRunHost struct {
	recorder *freshRunLifecycleRecorder
}

func (host recordingRunHost) StagePayload(runContext, []manifestPayload) error {
	host.recorder.add("setup.stage-payload")
	return nil
}

func (host recordingRunHost) CollectArtifacts(runContext, scenarioContract) error {
	host.recorder.add("settle.collect-artifacts")
	return nil
}

type recordingSessionBroker struct {
	recorder *freshRunLifecycleRecorder
	result   ScenarioResult
}

func (broker recordingSessionBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	broker.recorder.add("execute.run-scenario")
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("recording broker ran Scenario\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", broker.result.Status); err != nil {
		return ScenarioResult{}, err
	}
	return broker.result, nil
}

func writeFailureScreenshotRunConfigFixture(t *testing.T) string {
	t.Helper()
	dir := writeRunConfigFixture(t)
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
`)
	return dir
}

type failingSessionBroker struct {
	message string
}

func (broker failingSessionBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{}, fmt.Errorf("%s", broker.message)
}

type failingScreenshotSessionBroker struct {
	message string
}

func (broker failingScreenshotSessionBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	return failingSessionBroker(broker).RunScenario(context, scenario)
}

func (failingScreenshotSessionBroker) CaptureScreenshot(context runContext, request screenshotRequest) (visualEvidenceArtifact, error) {
	return fakeSessionBroker{}.CaptureScreenshot(context, request)
}
