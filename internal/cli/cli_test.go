package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

	code := RunWithRuntime([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
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
		"mayaScripts:maya/smoke.py":      filepath.Join("payload", "mayaScripts", "maya", "smoke.py"),
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

func TestScreenshotCapturesVisualEvidenceArtifact(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"screenshot"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("screenshot exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "artifact: screenshots/screenshot.png") {
		t.Fatalf("screenshot output missing artifact path:\n%s", stdout.String())
	}

	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	if _, err := os.Stat(filepath.Join(evidence, "screenshots", "screenshot.png")); err != nil {
		t.Fatalf("expected screenshot artifact: %v", err)
	}
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.VisualEvidence) != 1 {
		t.Fatalf("visual evidence count = %d, want 1", len(bundle.VisualEvidence))
	}
	got := bundle.VisualEvidence[0]
	if got.Kind != "screenshot" || got.Path != filepath.ToSlash(filepath.Join("screenshots", "screenshot.png")) {
		t.Fatalf("visual evidence metadata = %+v", got)
	}
}

func TestRecordCapturesVisualEvidenceArtifact(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"record"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("record exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "artifact: recordings/recording.mp4") {
		t.Fatalf("record output missing artifact path:\n%s", stdout.String())
	}

	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	recordingPath := filepath.Join(evidence, "recordings", "recording.mp4")
	if _, err := os.Stat(recordingPath); err != nil {
		t.Fatalf("expected recording artifact: %v", err)
	}
	recordingBytes, err := os.ReadFile(recordingPath)
	if err != nil {
		t.Fatalf("read recording artifact: %v", err)
	}
	wantDefaults := fmt.Sprintf("duration=%s fps=%d", defaultRecordingDuration, defaultRecordingFPS)
	if !strings.Contains(string(recordingBytes), wantDefaults) {
		t.Fatalf("recording artifact missing Crabbox-like defaults %q:\n%s", wantDefaults, string(recordingBytes))
	}
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.VisualEvidence) != 1 {
		t.Fatalf("visual evidence count = %d, want 1", len(bundle.VisualEvidence))
	}
	got := bundle.VisualEvidence[0]
	if got.Kind != "recording" || got.Path != filepath.ToSlash(filepath.Join("recordings", "recording.mp4")) {
		t.Fatalf("visual evidence metadata = %+v", got)
	}
}

func TestStandaloneVisualEvidenceCommandsReturnUsageCodeForUnknownTargetProfile(t *testing.T) {
	for _, command := range []string{"screenshot", "record"} {
		t.Run(command, func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			var stdout, stderr bytes.Buffer

			code := Run([]string{command, "--target-profile", "missing"}, &stdout, &stderr, dir, "test-version")
			if code != 2 {
				t.Fatalf("%s exit code = %d, want 2; stdout: %s stderr: %s", command, code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "unknown Target Profile") {
				t.Fatalf("%s error missing usage detail: %s", command, stderr.String())
			}
		})
	}
}

func TestEvidenceCollectWritesManifestAndExpectedVisualFiles(t *testing.T) {
	dir := writeRunConfigFixture(t)
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/smoke.py"
      scenes:
        - "scenes/start.ma"
      pluginArtifacts:
        - "build/demo.mll"
    expectedOutputs:
      scenarioResult: "outputs/smoke-result.json"
    evidence:
      screenshots:
        enabled: true
    validators:
      - type: visualEvidence
        required: true
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: passed") {
		t.Fatalf("evidence collect output missing status:\n%s", stdout.String())
	}

	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	for _, path := range []string{
		filepath.Join(evidence, "evidence.json"),
		filepath.Join(evidence, "manifest.json"),
		filepath.Join(evidence, "events.jsonl"),
		filepath.Join(evidence, "logs", "session.log"),
		filepath.Join(evidence, "scenario-result.json"),
		filepath.Join(evidence, "screenshots", "smoke.png"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected evidence collect file %s: %v", path, err)
		}
	}
	bundle := readEvidenceBundle(t, evidence)
	if bundle.Status != resultStatusPassed {
		t.Fatalf("evidence status = %q, want passed", bundle.Status)
	}
	if len(bundle.VisualEvidence) != 1 || bundle.VisualEvidence[0].Kind != "screenshot" {
		t.Fatalf("visual evidence metadata = %+v", bundle.VisualEvidence)
	}
	if len(bundle.Validators) != 1 || bundle.Validators[0].Type != "visualEvidence" || bundle.Validators[0].Status != resultStatusPassed {
		t.Fatalf("validator metadata = %+v", bundle.Validators)
	}
}

func TestEvidenceCollectFailsClearlyWhenRequiredVisualEvidenceMissing(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: visualEvidence
        required: true
`)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = outputWritingBroker{}

	code := RunWithRuntime([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("evidence collect exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: failed") {
		t.Fatalf("evidence collect output missing failed status:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "validator: visualEvidence failed - Visual Evidence is missing") {
		t.Fatalf("evidence collect output missing clear Visual Evidence failure:\n%s", stdout.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if bundle.Status != resultStatusFailed {
		t.Fatalf("evidence status = %q, want failed", bundle.Status)
	}
	if len(bundle.Validators) != 1 || bundle.Validators[0].Status != resultStatusFailed || !strings.Contains(bundle.Validators[0].Message, "Visual Evidence is missing") {
		t.Fatalf("missing Visual Evidence failure = %+v", bundle.Validators)
	}
}

func TestEvidenceCollectSanitizesScenarioNameBeforeWritingVisualEvidence(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  "../escape":
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    evidence:
      screenshots:
        enabled: true
    validators:
      - type: visualEvidence
        required: true
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "../escape"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	if _, err := os.Stat(filepath.Join(evidence, "escape.png")); !os.IsNotExist(err) {
		t.Fatalf("unexpected escaped screenshot path stat err = %v", err)
	}
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.VisualEvidence) != 1 {
		t.Fatalf("visual evidence metadata = %+v", bundle.VisualEvidence)
	}
	if strings.Contains(bundle.VisualEvidence[0].Path, "..") || strings.Contains(bundle.VisualEvidence[0].Path, "/../") {
		t.Fatalf("visual evidence path was not sanitized: %+v", bundle.VisualEvidence[0])
	}
	if _, err := os.Stat(filepath.Join(evidence, bundle.VisualEvidence[0].Path)); err != nil {
		t.Fatalf("expected sanitized visual evidence file: %v", err)
	}
}

func TestEvidencePublishCopiesBundleAndWritesReviewLinks(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = outputWritingBroker{
		files:          map[string]string{"outputs/report.json": `{"ok":true}` + "\n"},
		visualEvidence: true,
	}

	code := RunWithRuntime([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	runID := filepath.Base(evidence)
	store := filepath.Join(t.TempDir(), "evidence-store")
	baseURL := "https://evidence.example.test/maya/"

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", baseURL, evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	published := filepath.Join(store, runID)
	for _, path := range []string{
		filepath.Join(published, "evidence.json"),
		filepath.Join(published, "manifest.json"),
		filepath.Join(published, "artifact-manifest.json"),
		filepath.Join(published, "review-comment.md"),
		filepath.Join(published, "screenshots", "smoke.png"),
		filepath.Join(published, "outputs", "report.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected published artifact %s: %v", path, err)
		}
	}
	if info, err := os.Stat(published); err != nil {
		t.Fatalf("stat published Evidence Bundle: %v", err)
	} else if info.Mode().Perm()&0o055 != 0o055 {
		t.Fatalf("published Evidence Bundle mode = %v, want traversable by group/other", info.Mode().Perm())
	}

	artifactManifestBytes, err := os.ReadFile(filepath.Join(published, "artifact-manifest.json"))
	if err != nil {
		t.Fatalf("read artifact manifest: %v", err)
	}
	var artifactManifest publishedArtifactManifest
	if err := json.Unmarshal(artifactManifestBytes, &artifactManifest); err != nil {
		t.Fatalf("parse artifact manifest: %v", err)
	}
	wantScreenshotURL := "https://evidence.example.test/maya/" + runID + "/screenshots/smoke.png"
	wantLogURL := "https://evidence.example.test/maya/" + runID + "/logs/session.log"
	wantOutputURL := "https://evidence.example.test/maya/" + runID + "/outputs/report.json"
	if artifactManifest.RunID != runID || artifactManifest.BaseURL != strings.TrimRight(baseURL, "/") {
		t.Fatalf("artifact manifest identity = %+v, want run/base URL", artifactManifest)
	}
	if !publishedManifestHasURL(artifactManifest, "Visual Evidence", wantScreenshotURL) {
		t.Fatalf("artifact manifest missing screenshot URL %q: %+v", wantScreenshotURL, artifactManifest.Artifacts)
	}
	if !publishedManifestHasURL(artifactManifest, "logs", wantLogURL) {
		t.Fatalf("artifact manifest missing log URL %q: %+v", wantLogURL, artifactManifest.Artifacts)
	}
	if !publishedManifestHasURL(artifactManifest, "outputs", wantOutputURL) {
		t.Fatalf("artifact manifest missing output URL %q: %+v", wantOutputURL, artifactManifest.Artifacts)
	}

	reviewMarkdownBytes, err := os.ReadFile(filepath.Join(published, "review-comment.md"))
	if err != nil {
		t.Fatalf("read review markdown: %v", err)
	}
	reviewMarkdown := string(reviewMarkdownBytes)
	for _, want := range []string{
		"status: passed",
		wantScreenshotURL,
		wantLogURL,
		wantOutputURL,
		"https://evidence.example.test/maya/" + runID + "/evidence.json",
	} {
		if !strings.Contains(reviewMarkdown, want) {
			t.Fatalf("review markdown missing %q:\n%s", want, reviewMarkdown)
		}
	}

	mustWriteFile(t, filepath.Join(published, "stale.txt"), "stale\n")
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", baseURL, evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("republish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(published, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("republish did not remove stale file, stat err = %v", err)
	}
}

func TestEvidencePublishRejectsDestinationInsideBundle(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	nestedStore := filepath.Join(evidence, "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", nestedStore, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("nested publish exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "must not overlap") {
		t.Fatalf("nested publish error missing clear detail: %s", stderr.String())
	}
	if _, err := os.Stat(nestedStore); !os.IsNotExist(err) {
		t.Fatalf("nested publish created destination, stat err = %v", err)
	}
}

func TestEvidencePublishRejectsBundleInsideDestinationRun(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	runID := filepath.Base(evidence)
	store := filepath.Join(t.TempDir(), "store")
	bundleInsidePublishedRun := filepath.Join(store, runID, "bundle")
	if err := copyPath(evidence, bundleInsidePublishedRun); err != nil {
		t.Fatalf("prepare nested source bundle: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", bundleInsidePublishedRun}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("overlap publish exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "must not overlap") {
		t.Fatalf("overlap publish error missing clear detail: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(bundleInsidePublishedRun, "evidence.json")); err != nil {
		t.Fatalf("overlap publish deleted source bundle: %v", err)
	}
}

func TestEvidencePublishDoesNotDeleteSourceAtScratchLikePath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	runID := filepath.Base(evidence)
	store := filepath.Join(t.TempDir(), "store")
	scratchLikeSource := filepath.Join(store, "."+runID+".tmp")
	if err := copyPath(evidence, scratchLikeSource); err != nil {
		t.Fatalf("prepare scratch-like source bundle: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", scratchLikeSource}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(scratchLikeSource, "evidence.json")); err != nil {
		t.Fatalf("publish deleted scratch-like source bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store, runID, "review-comment.md")); err != nil {
		t.Fatalf("publish did not create final review comment: %v", err)
	}
}

func TestEvidencePublishInvalidBundleRunIDReturnsUsageCode(t *testing.T) {
	dir := t.TempDir()
	bundleDir := filepath.Join(dir, "bundle")
	mustWriteFile(t, filepath.Join(bundleDir, "evidence.json"), `{
  "runId": "../bad",
  "scenario": "smoke",
  "status": "passed",
  "targetProfile": "default",
  "host": "fake-local",
  "manifest": "manifest.json",
  "events": "events.jsonl",
  "log": "logs/session.log",
  "scenarioResult": "scenario-result.json",
  "payload": []
}
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "publish", "--destination", filepath.Join(dir, "store"), "--base-url", "https://evidence.example.test/maya", bundleDir}, &stdout, &stderr, dir, "test-version")
	if code != 2 {
		t.Fatalf("publish exit code = %d, want 2; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "run id") {
		t.Fatalf("publish error missing run id usage detail: %s", stderr.String())
	}
}

func TestEvidencePublishEscapesReviewMarkdownLabels(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = outputWritingBroker{
		files: map[string]string{"outputs/report](https://evil.test).json": "{}\n"},
	}

	code := RunWithRuntime([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	store := filepath.Join(t.TempDir(), "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := filepath.Base(evidence)
	reviewMarkdownBytes, err := os.ReadFile(filepath.Join(store, runID, "review-comment.md"))
	if err != nil {
		t.Fatalf("read review markdown: %v", err)
	}
	reviewMarkdown := string(reviewMarkdownBytes)
	if strings.Contains(reviewMarkdown, "](https://evil.test)") || strings.Contains(reviewMarkdown, "](https:/evil.test)") {
		t.Fatalf("review markdown contains unescaped forged link:\n%s", reviewMarkdown)
	}
	if !strings.Contains(reviewMarkdown, `outputs/report\]\(https:/evil.test\).json`) {
		t.Fatalf("review markdown missing escaped output label:\n%s", reviewMarkdown)
	}
	if !strings.Contains(reviewMarkdown, `](<https://evidence.example.test/maya/`) {
		t.Fatalf("review markdown missing angle-bracket link destination:\n%s", reviewMarkdown)
	}
}

func TestEvidencePublishFailedRepublishPreservesExistingRun(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    evidence:
      screenshots:
        enabled: true
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	runID := filepath.Base(evidence)
	store := filepath.Join(t.TempDir(), "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("initial publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	published := filepath.Join(store, runID)
	mustWriteFile(t, filepath.Join(published, "existing-marker.txt"), "keep me\n")
	bundle := readEvidenceBundle(t, evidence)
	bundle.VisualEvidence = []visualEvidenceArtifact{{Kind: "screenshot", Path: "screenshots/missing.png", MediaType: "image/png"}}
	if err := writeJSONFile(filepath.Join(evidence, "evidence.json"), bundle); err != nil {
		t.Fatalf("corrupt source evidence bundle: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("failed republish exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(published, "existing-marker.txt")); err != nil {
		t.Fatalf("failed republish did not preserve existing published run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(published, "review-comment.md")); err != nil {
		t.Fatalf("failed republish removed existing review comment: %v", err)
	}
}

func TestEvidencePublishLinksDeclaredOutputsOutsideOutputsDir(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: outputExists
        path: "reports/report.json"
`)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = outputWritingBroker{
		files: map[string]string{"reports/report.json": "{}\n"},
	}

	code := RunWithRuntime([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	if _, err := os.Stat(filepath.Join(evidence, "reports", "report.json")); err != nil {
		t.Fatalf("expected declared output in Evidence Bundle: %v", err)
	}
	store := filepath.Join(t.TempDir(), "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	published := filepath.Join(store, filepath.Base(evidence))
	wantOutputURL := "https://evidence.example.test/maya/" + filepath.Base(evidence) + "/reports/report.json"
	artifactManifestBytes, err := os.ReadFile(filepath.Join(published, "artifact-manifest.json"))
	if err != nil {
		t.Fatalf("read artifact manifest: %v", err)
	}
	var artifactManifest publishedArtifactManifest
	if err := json.Unmarshal(artifactManifestBytes, &artifactManifest); err != nil {
		t.Fatalf("parse artifact manifest: %v", err)
	}
	if !publishedManifestHasURL(artifactManifest, "outputs", wantOutputURL) {
		t.Fatalf("artifact manifest missing declared output URL %q: %+v", wantOutputURL, artifactManifest.Artifacts)
	}
	reviewMarkdownBytes, err := os.ReadFile(filepath.Join(published, "review-comment.md"))
	if err != nil {
		t.Fatalf("read review markdown: %v", err)
	}
	if !strings.Contains(string(reviewMarkdownBytes), wantOutputURL) {
		t.Fatalf("review markdown missing declared output URL %q:\n%s", wantOutputURL, string(reviewMarkdownBytes))
	}
}

func TestEvidencePublishEscapesReviewMarkdownMetadata(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	bundle.Status = "passed\n- forged: [x](https://evil.test)"
	bundle.Scenario = "smoke [scenario](https://evil.test)"
	if err := writeJSONFile(filepath.Join(evidence, "evidence.json"), bundle); err != nil {
		t.Fatalf("write malicious evidence metadata: %v", err)
	}
	store := filepath.Join(t.TempDir(), "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	reviewMarkdownBytes, err := os.ReadFile(filepath.Join(store, filepath.Base(evidence), "review-comment.md"))
	if err != nil {
		t.Fatalf("read review markdown: %v", err)
	}
	reviewMarkdown := string(reviewMarkdownBytes)
	if strings.Contains(reviewMarkdown, "\n- forged:") || strings.Contains(reviewMarkdown, "](https://evil.test)") {
		t.Fatalf("review markdown contains unescaped metadata:\n%s", reviewMarkdown)
	}
	if !strings.Contains(reviewMarkdown, `status: passed - forged: \[x\]\(https://evil.test\)`) {
		t.Fatalf("review markdown missing escaped metadata:\n%s", reviewMarkdown)
	}
}

func TestScreenshotReportsHostLockReleaseFailure(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = lockCorruptingVisualBroker{}

	code := RunWithRuntime([]string{"screenshot"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("screenshot exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "release Host Lock for fake-local") {
		t.Fatalf("screenshot error did not report Host Lock release failure: %s", stderr.String())
	}
}

func TestRunScenarioProvidesScenarioResultPathInRunEnvironment(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	broker := &environmentCheckingBroker{}
	runtime := defaultRunRuntime()
	runtime.Broker = broker

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if broker.resultPath == "" {
		t.Fatal("broker did not receive Scenario Result path")
	}
	wantSuffix := filepath.Join(".maya-stall", "state", "runs", broker.runID, "workspace", "outputs", "smoke-result.json")
	if !strings.HasSuffix(broker.resultPath, wantSuffix) {
		t.Fatalf("Scenario Result path = %q, want suffix %q", broker.resultPath, wantSuffix)
	}
	if broker.resultPath != broker.environment[scenarioResultEnvVar] {
		t.Fatalf("run environment %s = %q, want %q", scenarioResultEnvVar, broker.environment[scenarioResultEnvVar], broker.resultPath)
	}
}

func TestRunScenarioUsesScenarioAuthoredResultFile(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = scenarioResultFileBroker{}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: failed") {
		t.Fatalf("run output missing file-authored failed status:\n%s", stdout.String())
	}

	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	scenarioResultBytes, err := os.ReadFile(filepath.Join(evidence, "scenario-result.json"))
	if err != nil {
		t.Fatalf("read evidence Scenario Result: %v", err)
	}
	var scenarioResult map[string]any
	if err := json.Unmarshal(scenarioResultBytes, &scenarioResult); err != nil {
		t.Fatalf("parse evidence Scenario Result: %v", err)
	}
	if scenarioResult["status"] != "failed" || scenarioResult["summary"] != "script wrote result" {
		t.Fatalf("evidence Scenario Result status/summary = %#v", scenarioResult)
	}
	if !strings.Contains(string(scenarioResultBytes), `"largeNumber": 9007199254740993`) {
		t.Fatalf("evidence Scenario Result rounded large number:\n%s", string(scenarioResultBytes))
	}
	assertions, ok := scenarioResult["assertions"].([]any)
	if !ok || len(assertions) != 1 {
		t.Fatalf("evidence Scenario Result lost assertions: %#v", scenarioResult)
	}
}

func TestRunScenarioAuthoredResultWithoutStatusKeepsBrokerFailure(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = partialScenarioResultFileBroker{}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: failed") {
		t.Fatalf("run output missing broker failure status:\n%s", stdout.String())
	}

	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	scenarioResultBytes, err := os.ReadFile(filepath.Join(evidence, "scenario-result.json"))
	if err != nil {
		t.Fatalf("read evidence Scenario Result: %v", err)
	}
	if !strings.Contains(string(scenarioResultBytes), `"status": "failed"`) {
		t.Fatalf("evidence Scenario Result did not preserve broker failure:\n%s", string(scenarioResultBytes))
	}
	if !strings.Contains(string(scenarioResultBytes), `"summary": "script partial result"`) {
		t.Fatalf("evidence Scenario Result lost authored summary:\n%s", string(scenarioResultBytes))
	}
}

func TestRunScenarioRejectsScenarioResultWithTrailingJSON(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = trailingScenarioResultBroker{}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "parse Scenario Result") {
		t.Fatalf("run error did not reject trailing JSON: %s", stderr.String())
	}
}

func TestRunScenarioRejectsSymlinkedScenarioResult(t *testing.T) {
	dir := writeRunConfigFixture(t)
	outside := filepath.Join(t.TempDir(), "result.json")
	mustWriteFile(t, outside, `{"status":"passed","summary":"outside"}`+"\n")
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = symlinkOutputBroker{linkPath: "outputs/smoke-result.json", target: outside}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Scenario Result path") || !strings.Contains(stderr.String(), "must not be or contain a symlink") {
		t.Fatalf("run error did not reject symlinked Scenario Result: %s", stderr.String())
	}
}

func TestRunScenarioStagesTypedPayloadIncludingExplicitIgnoredOutputs(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('smoke')\n")
	mustWriteFile(t, filepath.Join(dir, "scenes", "start.ma"), "// fake maya scene\n")
	mustWriteFile(t, filepath.Join(dir, "build", "demo.mll"), "fake ignored plugin artifact\n")
	mustWriteFile(t, filepath.Join(dir, "golden", "expected.json"), `{"ok":true}`+"\n")
	mustWriteFile(t, filepath.Join(dir, "includes", "helpers.py"), "def helper(): pass\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    mayaVersion: "2025"
    payload:
      mayaScripts:
        - "maya/smoke.py"
      scenes:
        - "scenes/start.ma"
      pluginArtifacts:
        - "build/demo.mll"
      expectedOutputs:
        - "golden/expected.json"
      includePaths:
        - "includes"
    expectedOutputs:
      scenarioResult: "outputs/smoke-result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	runState := onlyRunDir(t, filepath.Join(dir, ".maya-stall", "state", "runs"))
	manifestBytes, err := os.ReadFile(filepath.Join(runState, "manifest.json"))
	if err != nil {
		t.Fatalf("read run manifest: %v", err)
	}
	var manifest struct {
		Payload []manifestPayload `json:"payload"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse run manifest: %v", err)
	}
	wantPayload := map[string]string{
		"mayaScripts:maya/smoke.py":            filepath.Join("payload", "mayaScripts", "maya", "smoke.py"),
		"scenes:scenes/start.ma":               filepath.Join("payload", "scenes", "scenes", "start.ma"),
		"pluginArtifacts:build/demo.mll":       filepath.Join("payload", "pluginArtifacts", "build", "demo.mll"),
		"expectedOutputs:golden/expected.json": filepath.Join("payload", "expectedOutputs", "golden", "expected.json"),
		"includePaths:includes":                filepath.Join("payload", "includePaths", "includes"),
	}
	for _, item := range manifest.Payload {
		key := item.Kind + ":" + item.Source
		want, ok := wantPayload[key]
		if !ok {
			t.Fatalf("unexpected payload item: %+v", item)
		}
		if item.Staged != want {
			t.Fatalf("staged path for %s = %q, want %q", key, item.Staged, want)
		}
		if _, err := os.Stat(filepath.Join(runState, item.Staged)); err != nil {
			t.Fatalf("staged payload %s: %v", item.Staged, err)
		}
		delete(wantPayload, key)
	}
	if len(wantPayload) != 0 {
		t.Fatalf("manifest missing payload items: %#v", wantPayload)
	}
}

func TestRunScenarioReportsMissingTypedPayloadPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      includePaths:
        - "missing/includes"
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "stage includePaths payload missing/includes") {
		t.Fatalf("missing typed payload error = %q", stderr.String())
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

func TestDoctorRealSSHVerifiesConnectivityAndWritableWorkRoot(t *testing.T) {
	dir := writeRunConfigFixture(t)
	sshLog := filepath.Join(dir, "ssh.log")
	sshPath := writeFakeCommand(t, dir, "fake-ssh", sshLog, 0)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: fake-alpha
        health: healthy
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          user: maya-runner
          port: 2222
          identityFile: ~/.ssh/maya-stall-ci
          binary: `+strconv.Quote(sshPath)+`
        workRoot: C:/maya-stall
        broker: ok
        mayaVersions: ["2025"]
        visualEvidence: true
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"ssh: ok - reachable",
		"work-root: ok - writable",
		"session-broker: ok - reachable",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
	logBytes, err := os.ReadFile(sshLog)
	if err != nil {
		t.Fatalf("read ssh log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{"-p", "2222", "-i", filepath.Join(os.Getenv("HOME"), ".ssh", "maya-stall-ci"), "maya-runner@maya-win-01", "powershell"} {
		if !strings.Contains(log, want) {
			t.Fatalf("ssh command log missing %q:\n%s", want, log)
		}
	}
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=10"} {
		if !strings.Contains(log, want) {
			t.Fatalf("ssh command log missing noninteractive option %q:\n%s", want, log)
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
			wantHint:   "Choose a configured Target Profile or add it to the host config. See docs/setup/windows-maya-host.md#target-profile-and-host-pool.",
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
			wantHint:   "Add at least one Maya Host to the Host Pool. See docs/setup/windows-maya-host.md#target-profile-and-host-pool.",
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
			wantHint:   "Fix SSH reachability for this Maya Host. See docs/setup/windows-maya-host.md#openssh-reachability.",
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
			wantHint:   "Fix the host work root path or permissions. See docs/setup/windows-maya-host.md#work-root.",
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
			wantHint:   "Start or repair the Session Broker on this Maya Host. See docs/setup/windows-maya-host.md#session-broker.",
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
			wantHint:   "Install a compatible Autodesk Maya version or choose another Maya Host. See docs/setup/windows-maya-host.md#autodesk-maya.",
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
			wantHint:   "Enable screenshot or recording capture through the Session Broker. See docs/setup/windows-maya-host.md#visual-evidence.",
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
			wantHint:   "Wait for the active Fresh Run or clear the stale Host Lock after verifying no run is active. See docs/setup/windows-maya-host.md#host-lock-and-retention.",
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
			wantHint:   "Fix the Scenario payload paths and expectedOutputs.scenarioResult in repo config. See docs/setup/windows-maya-host.md#scenario-inputs.",
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

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
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

func TestRunScenarioRealSSHUploadsPayloadAndDownloadsDeclaredOutputs(t *testing.T) {
	dir := writeRunConfigFixture(t)
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	content := strings.Replace(string(contentBytes), "files: []", "files:\n        - \"outputs/report.json\"", 1)
	content = strings.Replace(content, "recording:\n        enabled: false", "recording:\n        enabled: false\n    validators:\n      - type: outputExists\n        path: \"outputs/report.json\"", 1)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	sftpLog := filepath.Join(dir, "sftp.log")
	sftpPath := writeFakeSFTPCommand(t, dir, sftpLog)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: alpha") || !strings.Contains(stdout.String(), "status: passed") {
		t.Fatalf("run output missing selected real host/pass status:\n%s", stdout.String())
	}
	logBytes, err := os.ReadFile(sftpLog)
	if err != nil {
		t.Fatalf("read sftp log: %v", err)
	}
	log := string(logBytes)
	for _, want := range []string{
		`-mkdir "C:/maya-stall"`,
		`-mkdir "C:/maya-stall/runs"`,
		`put -r `,
		`maya/smoke.py`,
		`C:/maya-stall/runs/`,
		`payload/mayaScripts/maya/smoke.py`,
		`-get -r `,
		`workspace/outputs/report.json`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("sftp batch missing %q:\n%s", want, log)
		}
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	if _, err := os.Stat(filepath.Join(evidence, "outputs", "report.json")); err != nil {
		t.Fatalf("expected downloaded output in Evidence Bundle: %v", err)
	}
}

func TestRunScenarioRealSSHRequiresDownloadedScenarioResult(t *testing.T) {
	dir := writeRunConfigFixture(t)
	sftpPath := writeFailingGetSFTPCommand(t, dir)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "download declared outputs") {
		t.Fatalf("run error did not report required Scenario Result download failure: %s", stderr.String())
	}
}

func TestRunScenarioRealSSHReportsSFTPStderr(t *testing.T) {
	dir := writeRunConfigFixture(t)
	sftpPath := writeFailingCommandWithStderr(t, dir, "fake-sftp-stderr", "remote disk full")
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "remote disk full") {
		t.Fatalf("run error missing sftp stderr: %s", stderr.String())
	}
}

func TestWithStderrTailStripsControlCharacters(t *testing.T) {
	err := withStderrTail(fmt.Errorf("ssh command failed"), "ok\x1b]52;c;secret\x07\rspoof\u009dhidden\u009c\u202Eevil\nnext")
	message := err.Error()
	if strings.ContainsAny(message, "\x1b\x07\r\u009d\u009c\u202E") {
		t.Fatalf("stderr tail kept terminal control characters: %q", message)
	}
	for _, want := range []string{"ssh command failed", "ok]52;c;secretspoofhiddenevil", "next"} {
		if !strings.Contains(message, want) {
			t.Fatalf("stderr tail missing %q: %q", want, message)
		}
	}
}

func TestSFTPBatchTimeoutConfig(t *testing.T) {
	timeout, err := sftpBatchTimeout(mayaHostConfig{})
	if err != nil {
		t.Fatalf("default sftp timeout: %v", err)
	}
	if timeout != defaultSFTPBatchTimeout {
		t.Fatalf("default sftp timeout = %s, want %s", timeout, defaultSFTPBatchTimeout)
	}

	timeout, err = sftpBatchTimeout(mayaHostConfig{SSH: sshConfig{SFTPTimeout: "0"}})
	if err != nil {
		t.Fatalf("disabled sftp timeout: %v", err)
	}
	if timeout != 0 {
		t.Fatalf("disabled sftp timeout = %s, want 0", timeout)
	}

	if _, err := sftpBatchTimeout(mayaHostConfig{SSH: sshConfig{SFTPTimeout: "-1s"}}); err == nil {
		t.Fatal("negative sftp timeout succeeded")
	}
}

func TestRunScenarioRealSSHDownloadsValidatorOnlyOutputs(t *testing.T) {
	dir := writeRunConfigFixture(t)
	configPath := filepath.Join(dir, ".maya-stall.yaml")
	contentBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config fixture: %v", err)
	}
	content := strings.Replace(string(contentBytes), "recording:\n        enabled: false", "recording:\n        enabled: false\n    validators:\n      - type: jsonEquals\n        path: \"outputs/metrics.json\"\n        jsonPath: \"$.status\"\n        equals: passed", 1)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	sftpLog := filepath.Join(dir, "sftp.log")
	sftpPath := writeFakeSFTPCommand(t, dir, sftpLog)
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	logBytes, err := os.ReadFile(sftpLog)
	if err != nil {
		t.Fatalf("read sftp log: %v", err)
	}
	if !strings.Contains(string(logBytes), "workspace/outputs/metrics.json") {
		t.Fatalf("sftp batch did not download validator-only output:\n%s", string(logBytes))
	}
}

func TestRunScenarioRealSSHRejectsNestedSymlinkPayload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "leak.py"), "print('outside')\n")
	if err := os.MkdirAll(filepath.Join(dir, "includes"), 0o755); err != nil {
		t.Fatalf("create includes fixture: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "leak.py"), filepath.Join(dir, "includes", "leak.py")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload:
      includePaths:
        - "includes"
    expectedOutputs:
      scenarioResult: "outputs/result.json"
`)
	sftpPath := writeFakeSFTPCommand(t, dir, filepath.Join(dir, "sftp.log"))
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Fatalf("run error did not reject nested symlink payload: %s", stderr.String())
	}
}

func TestRunScenarioRealSSHRejectsSFTPBatchControlCharacters(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('smoke')\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), "version: 1\nscenarios:\n  smoke:\n    payload:\n      scripts:\n        - \"maya/smoke.py\"\n    expectedOutputs:\n      files:\n        - \"outputs/report.json\\nput /tmp/leak C:/maya-stall/leak\"\n      scenarioResult: \"outputs/result.json\"\n")
	sftpPath := writeFakeSFTPCommand(t, dir, filepath.Join(dir, "sftp.log"))
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "control characters") {
		t.Fatalf("run error did not reject SFTP batch control characters: %s", stderr.String())
	}
}

func TestRunScenarioRealSSHRejectsBackslashRemoteArtifactPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('smoke')\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), "version: 1\nscenarios:\n  smoke:\n    payload:\n      scripts:\n        - \"maya/smoke.py\"\n    expectedOutputs:\n      files:\n        - \"..\\\\..\\\\Users\\\\maya-runner\\\\secret.json\"\n      scenarioResult: \"outputs/result.json\"\n")
	sftpPath := writeFakeSFTPCommand(t, dir, filepath.Join(dir, "sftp.log"))
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:\maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "backslashes") {
		t.Fatalf("run error did not reject backslash artifact path: %s", stderr.String())
	}
}

func TestSFTPBatchMkdirAllPreservesAbsolutePOSIXRoot(t *testing.T) {
	batch := newSFTPBatch()
	batch.mkdirAll("/opt/maya-stall/runs")

	for _, want := range []string{
		`-mkdir "/opt"`,
		`-mkdir "/opt/maya-stall"`,
		`-mkdir "/opt/maya-stall/runs"`,
	} {
		if !strings.Contains(batch.String(), want) {
			t.Fatalf("sftp mkdirAll missing %q:\n%s", want, batch.String())
		}
	}
	if strings.Contains(batch.String(), `-mkdir "opt"`) {
		t.Fatalf("sftp mkdirAll emitted relative POSIX path:\n%s", batch.String())
	}
}

func TestRunScenarioRealSSHRejectsSymlinkedDownloadDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := writeRunConfigFixture(t)
	sftpPath := writeFakeSFTPCommand(t, dir, filepath.Join(dir, "sftp.log"))
	hostConfigPath := filepath.Join(dir, "ci-hosts.yaml")
	mustWriteFile(t, hostConfigPath, `version: 1
targetProfiles:
  ci:
    hostPool: windows-maya
hostPools:
  windows-maya:
    hosts:
      - id: alpha
        transport: ssh
        ssh:
          host: maya-win-01
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
`)
	runtimeConfig := defaultRunRuntime()
	runtimeConfig.Broker = symlinkOutputBroker{linkPath: "outputs", target: t.TempDir()}
	var stdout, stderr bytes.Buffer

	code := RunWithRuntime([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version", runtimeConfig)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "symlink") {
		t.Fatalf("run error did not reject symlinked download destination: %s", stderr.String())
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

func TestRunScenarioDoesNotWaitWhenNoHealthyHostExists(t *testing.T) {
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
`)
	var stdout, stderr bytes.Buffer

	start := time.Now()
	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-wait", "2s", "smoke"}, &stdout, &stderr, dir, "test-version")
	elapsed := time.Since(start)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("run waited %s despite no healthy host", elapsed)
	}
	if !strings.Contains(stderr.String(), "no healthy Maya Host") {
		t.Fatalf("run error missing no healthy host detail: %s", stderr.String())
	}
}

func TestRunScenarioReportsStaleHostLockReapGuard(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	guardPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "reap", "alpha.lock")
	mustWriteFile(t, guardPath, "host: alpha\npid: 99999999\n")
	var stdout, stderr bytes.Buffer

	start := time.Now()
	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "--host-lock-wait", "2s", "smoke"}, &stdout, &stderr, dir, "test-version")
	elapsed := time.Since(start)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("run waited %s despite stale reap guard requiring manual cleanup", elapsed)
	}
	for _, want := range []string{"Host Lock reap guard", "stale guard", guardPath, "remove it after verifying"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("run error missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestDoctorReportsStaleHostLockReapGuard(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	guardPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "reap", "alpha.lock")
	mustWriteFile(t, guardPath, "host: alpha\npid: 99999999\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "alpha"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("doctor exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"host-lock: fail", "Host Lock reap guard", guardPath} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestDoctorRejectsInvalidSFTPTimeout(t *testing.T) {
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
        transport: ssh
        ssh:
          host: maya-win-01
          sftpTimeout: 30min
        workRoot: C:/maya-stall
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"doctor", "--host-config", hostConfigPath, "--target-profile", "ci", "--host", "alpha"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("doctor exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"ssh: fail", "sftpTimeout", "30min", "work-root: fail - skipped"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("doctor output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunScenarioSkipsHostWithStaleReapGuard(t *testing.T) {
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
	mustWriteFile(t, filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "reap", "alpha.lock"), "host: alpha\npid: 99999999\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "host: beta") {
		t.Fatalf("run output missing fallback host:\n%s", stdout.String())
	}
}

func TestHostLockTreatsLiveReapGuardAsLocked(t *testing.T) {
	dir := t.TempDir()
	guardPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "reap", "alpha.lock")
	mustWriteFile(t, guardPath, fmt.Sprintf("host: alpha\npid: %d\n", os.Getpid()))

	release, locked, err := acquireHostLock(dir, "alpha")
	if err != nil {
		t.Fatalf("acquire with live reap guard: %v", err)
	}
	if !locked {
		defer release()
		t.Fatal("host was not reported locked while reap guard was live")
	}
}

func TestHostLockTreatsFreshEmptyReapGuardAsLocked(t *testing.T) {
	dir := t.TempDir()
	guardPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "reap", "alpha.lock")
	mustWriteFile(t, guardPath, "")

	release, locked, err := acquireHostLock(dir, "alpha")
	if err != nil {
		t.Fatalf("acquire with fresh reap guard: %v", err)
	}
	if !locked {
		defer release()
		t.Fatal("host was not reported locked while reap guard was being populated")
	}
}

func TestHostLockReapGuardDoesNotCollideWithDottedHostID(t *testing.T) {
	dir := t.TempDir()
	releaseDotted, locked, err := acquireHostLock(dir, "alpha.reap")
	if err != nil {
		t.Fatalf("lock dotted host: %v", err)
	}
	if locked {
		t.Fatal("dotted host was unexpectedly locked")
	}
	defer releaseDotted()

	releaseAlpha, locked, err := acquireHostLock(dir, "alpha")
	if err != nil {
		t.Fatalf("lock alpha host: %v", err)
	}
	if locked {
		t.Fatal("alpha host was blocked by alpha.reap lock")
	}
	defer releaseAlpha()
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

func TestRunScenarioCleansUpFreshRunStateAndReleasesHostLockByDefault(t *testing.T) {
	dir := writeRunConfigFixture(t)
	hostConfigPath := writeSingleHealthyHostConfig(t, dir)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "stopPolicy: stopped") {
		t.Fatalf("run output missing stopped Stop Policy:\n%s", stdout.String())
	}
	onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	runsDir := filepath.Join(dir, ".maya-stall", "state", "runs")
	if entries, err := os.ReadDir(runsDir); err == nil && len(entries) != 0 {
		t.Fatalf("run state entries after default cleanup = %d, want 0", len(entries))
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read run state after cleanup: %v", err)
	}
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", "alpha.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("host lock after default cleanup = %v, want missing", err)
	}
}

func TestKeepOnFailureLeavesSessionForStatusAttachAndStop(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	runtime := defaultRunRuntime()
	runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: "failed", Summary: "fake failure"}}

	code := RunWithRuntime([]string{"run", "--keep-on-failure", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	for _, want := range []string{
		"status: failed",
		"stopPolicy: kept",
		"next: maya-stall status --run " + runID,
		"next: maya-stall attach " + runID,
		"next: maya-stall stop " + runID,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("run output missing %q:\n%s", want, stdout.String())
		}
	}
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("kept Host Lock missing: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"status", "--run", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("status exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{"run: " + runID, "state: kept", "host: " + defaultFakeHostID, "status: failed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"attach", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("attach exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	for _, want := range []string{"events:", "run.started", "broker.session.finished", "logs:", "fake Session Broker ran Scenario"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("attach output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"stop", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("stop exit code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "stopped: "+runID) {
		t.Fatalf("stop output missing run id:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", runID)); !os.IsNotExist(err) {
		t.Fatalf("kept run state after stop = %v, want missing", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("kept Host Lock after stop = %v, want missing", err)
	}
}

func TestStopReleasesKeptHostLockWhenEvidenceIsMissing(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("kept Host Lock missing: %v", err)
	}
	if err := os.Rename(filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json"), filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json.moved")); err != nil {
		t.Fatalf("move evidence fixture: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"stop", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("stop exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("kept Host Lock after stop = %v, want missing", err)
	}
}

func TestAttachWorksWhenEvidenceIsMissing(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	if err := os.Rename(filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json"), filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json.moved")); err != nil {
		t.Fatalf("move evidence fixture: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"attach", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("attach exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"events:", "run.started", "logs:", "fake Session Broker ran Scenario"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("attach output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestStatusShowsUnknownWhenEvidenceIsMissing(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	if err := os.Rename(filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json"), filepath.Join(dir, "artifacts", "maya-stall", runID, "evidence.json.moved")); err != nil {
		t.Fatalf("move evidence fixture: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"status", "--run", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("status exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"run: " + runID, "state: kept", "status: unknown", "evidence: unavailable"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestRunStateCommandsRejectDotRunIDs(t *testing.T) {
	for _, command := range []string{"status", "attach", "stop"} {
		for _, runID := range []string{".", ".."} {
			t.Run(command+" "+runID, func(t *testing.T) {
				dir := writeRunConfigFixture(t)
				var stdout, stderr bytes.Buffer
				args := []string{command}
				if command == "status" {
					args = append(args, "--run")
				}
				args = append(args, runID)

				code := Run(args, &stdout, &stderr, dir, "test-version")
				if code != 2 {
					t.Fatalf("%s exit code = %d, want 2; stdout: %s stderr: %s", command, code, stdout.String(), stderr.String())
				}
				if !strings.Contains(stderr.String(), "run id") {
					t.Fatalf("%s error missing run id detail: %s", command, stderr.String())
				}
			})
		}
	}
}

func TestStatusIgnoresPartialNonKeptRunState(t *testing.T) {
	dir := writeRunConfigFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, ".maya-stall", "state", "runs", "partial"), 0o755); err != nil {
		t.Fatalf("create partial run state: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"status"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("status exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "state: no kept sessions") {
		t.Fatalf("status output did not ignore partial state:\n%s", stdout.String())
	}
}

func TestStatusRejectsSymlinkedLockDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := writeRunConfigFixture(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".maya-stall", "state", "locks"), 0o755); err != nil {
		t.Fatalf("create lock parent: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".maya-stall", "state", "locks", "hosts")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := Run([]string{"status"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("status exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestTargetedStatusAndAttachRequireKeptHostLock(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	if err := os.Rename(lockPath, lockPath+".moved"); err != nil {
		t.Fatalf("move Host Lock fixture: %v", err)
	}

	for _, args := range [][]string{
		{"status", "--run", runID},
		{"attach", runID},
	} {
		stdout.Reset()
		stderr.Reset()
		code := Run(args, &stdout, &stderr, dir, "test-version")
		if code == 0 {
			t.Fatalf("%v exit code = 0, want failure; stdout: %s stderr: %s", args, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String(), "state: kept") {
			t.Fatalf("%v misreported stale state as kept:\n%s", args, stdout.String())
		}
	}
}

func TestStatusRejectsSymlinkedEvidenceParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "maya-stall", runID, "evidence.json"), `{"runId":"`+runID+`","status":"passed"}`+"\n")
	artifactsPath := filepath.Join(dir, "artifacts")
	if err := os.Rename(artifactsPath, artifactsPath+".real"); err != nil {
		t.Fatalf("move artifacts fixture: %v", err)
	}
	if err := os.Symlink(outside, artifactsPath); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"status", "--run", runID}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("status exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestStopDoesNotReleaseHostLockForDifferentKeptRun(t *testing.T) {
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	runID := runIDFromOutput(t, stdout.String())
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	mustWriteFile(t, lockPath, "host: "+defaultFakeHostID+"\nkeptRun: different-run\n")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"stop", runID}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("stop exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read Host Lock after mismatched stop: %v", err)
	}
	if !strings.Contains(string(content), "keptRun: different-run") {
		t.Fatalf("Host Lock was changed by mismatched stop:\n%s", string(content))
	}
}

func TestStopReleasesMatchingKeptHostLockWhenRunStateIsMissing(t *testing.T) {
	dir := writeRunConfigFixture(t)
	runID := "20260704T112500.000000000Z"
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	mustWriteFile(t, lockPath, "host: "+defaultFakeHostID+"\nkeptRun: "+runID+"\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"stop", runID}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("stop exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("kept Host Lock after orphan stop = %v, want missing", err)
	}
}

func TestStopRejectsSymlinkedRunStateParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := writeRunConfigFixture(t)
	runID := "20260704T112300.000000000Z"
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, runID, "manifest.json"), `{"runId":"`+runID+`","scenario":"smoke","targetProfile":"default","host":"`+defaultFakeHostID+`"}`+"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".maya-stall", "state"), 0o755); err != nil {
		t.Fatalf("create state parent: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".maya-stall", "state", "runs")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock")
	mustWriteFile(t, lockPath, "host: "+defaultFakeHostID+"\nkeptRun: "+runID+"\n")
	var stdout, stderr bytes.Buffer

	code := Run([]string{"stop", runID}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("stop exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outside, runID, "manifest.json")); err != nil {
		t.Fatalf("outside run state was removed or changed: %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("Host Lock was released after failed stop: %v", err)
	}
}

func TestCleanupRunStateRejectsSymlinkedRunStateParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := t.TempDir()
	runID := "20260704T112400.000000000Z"
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, runID, "sentinel.txt"), "keep me\n")
	if err := os.MkdirAll(filepath.Join(dir, ".maya-stall", "state"), 0o755); err != nil {
		t.Fatalf("create state parent: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".maya-stall", "state", "runs")); err != nil {
		t.Skipf("create symlink fixture: %v", err)
	}

	err := cleanupRunState(dir, runID)
	if err == nil {
		t.Fatal("cleanupRunState returned nil error for symlinked run state parent")
	}
	if _, statErr := os.Stat(filepath.Join(outside, runID, "sentinel.txt")); statErr != nil {
		t.Fatalf("outside run state was removed or changed: %v", statErr)
	}
}

func TestRunRejectsConflictingStopPolicyFlags(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--keep-on-failure", "--stop-after", "always", "smoke"},
		{"run", "--stop-after", "never", "--keep-on-failure", "smoke"},
	} {
		t.Run(strings.Join(args[1:len(args)-1], " "), func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			var stdout, stderr bytes.Buffer

			code := Run(args, &stdout, &stderr, dir, "test-version")
			if code != 2 {
				t.Fatalf("run exit code = %d, want 2; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "Stop Policy") {
				t.Fatalf("conflicting Stop Policy error = %q", stderr.String())
			}
		})
	}
}

func TestAttachRejectsSymlinkedEventOrLogFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	tests := []struct {
		name       string
		path       string
		linkParent bool
	}{
		{name: "events", path: "events.jsonl"},
		{name: "log", path: filepath.Join("logs", "session.log")},
		{name: "log parent", path: "logs", linkParent: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			var stdout, stderr bytes.Buffer
			code := Run([]string{"run", "--stop-after", "never", "smoke"}, &stdout, &stderr, dir, "test-version")
			if code != 0 {
				t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
			}
			runID := runIDFromOutput(t, stdout.String())
			target := filepath.Join(t.TempDir(), "secret.txt")
			if tt.linkParent {
				target = t.TempDir()
				mustWriteFile(t, filepath.Join(target, "session.log"), "do not print\n")
			} else {
				mustWriteFile(t, target, "do not print\n")
			}
			linkPath := filepath.Join(dir, ".maya-stall", "state", "runs", runID, tt.path)
			if err := os.Rename(linkPath, linkPath+".real"); err != nil {
				t.Fatalf("move %s fixture: %v", tt.path, err)
			}
			if err := os.Symlink(target, linkPath); err != nil {
				t.Skipf("create symlink fixture: %v", err)
			}

			stdout.Reset()
			stderr.Reset()
			code = Run([]string{"attach", runID}, &stdout, &stderr, dir, "test-version")
			if code != 1 {
				t.Fatalf("attach exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stdout.String(), "do not print") {
				t.Fatalf("attach printed symlink target:\n%s", stdout.String())
			}
		})
	}
}

func TestRunScenarioHonorsExplicitStopAfterPolicy(t *testing.T) {
	tests := []struct {
		name       string
		stopAfter  string
		result     string
		wantPolicy string
	}{
		{name: "success", stopAfter: "success", result: "passed", wantPolicy: "stopped"},
		{name: "success keeps failure", stopAfter: "success", result: "failed", wantPolicy: "kept"},
		{name: "failure", stopAfter: "failure", result: "failed", wantPolicy: "stopped"},
		{name: "failure keeps success", stopAfter: "failure", result: "passed", wantPolicy: "kept"},
		{name: "always", stopAfter: "always", result: "passed", wantPolicy: "stopped"},
		{name: "never", stopAfter: "never", result: "passed", wantPolicy: "kept"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			var stdout, stderr bytes.Buffer
			runtime := defaultRunRuntime()
			runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: tt.result, Summary: "fake result"}}

			code := RunWithRuntime([]string{"run", "--stop-after", tt.stopAfter, "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
			wantCode := 0
			if tt.result != resultStatusPassed {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("run exit code = %d, want %d; stdout: %s stderr: %s", code, wantCode, stdout.String(), stderr.String())
			}
			runID := runIDFromOutput(t, stdout.String())
			if !strings.Contains(stdout.String(), "stopPolicy: "+tt.wantPolicy) {
				t.Fatalf("run output missing Stop Policy %q:\n%s", tt.wantPolicy, stdout.String())
			}
			_, stateErr := os.Stat(filepath.Join(dir, ".maya-stall", "state", "runs", runID))
			_, lockErr := os.Stat(filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", defaultFakeHostID+".lock"))
			if tt.wantPolicy == "kept" {
				if stateErr != nil {
					t.Fatalf("kept run state missing: %v", stateErr)
				}
				if lockErr != nil {
					t.Fatalf("kept Host Lock missing: %v", lockErr)
				}
				return
			}
			if !os.IsNotExist(stateErr) {
				t.Fatalf("stopped run state = %v, want missing", stateErr)
			}
			if !os.IsNotExist(lockErr) {
				t.Fatalf("stopped Host Lock = %v, want missing", lockErr)
			}
		})
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

func TestRunScenarioResultStatusValidatorFailureDrivesRunStatusAndEvidenceMetadata(t *testing.T) {
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
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status: failed") {
		t.Fatalf("run output missing failed status:\n%s", stdout.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if bundle.Status != "failed" {
		t.Fatalf("evidence status = %q, want failed", bundle.Status)
	}
	scenarioResultBytes, err := os.ReadFile(filepath.Join(evidence, bundle.ScenarioResult))
	if err != nil {
		t.Fatalf("read evidence Scenario Result: %v", err)
	}
	var scenarioResult ScenarioResult
	if err := json.Unmarshal(scenarioResultBytes, &scenarioResult); err != nil {
		t.Fatalf("parse evidence Scenario Result: %v", err)
	}
	if scenarioResult.Status != "failed" {
		t.Fatalf("evidence Scenario Result status = %q, want failed", scenarioResult.Status)
	}
	if len(bundle.Validators) != 1 {
		t.Fatalf("validator result count = %d, want 1", len(bundle.Validators))
	}
	got := bundle.Validators[0]
	if got.Type != "scenarioResultStatus" || got.Status != "failed" || !strings.Contains(got.Message, `status "passed"`) {
		t.Fatalf("validator metadata = %+v", got)
	}
}

func TestRunScenarioValidatorsCanInspectScenarioResultOutput(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: jsonEquals
        path: "outputs/result.json"
        jsonPath: "$.status"
        equals: passed
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.Validators) != 1 || bundle.Validators[0].Status != "passed" {
		t.Fatalf("validator metadata = %+v", bundle.Validators)
	}
}

func TestRunScenarioValidatorsCoverOutputsJSONHashesAndVisualEvidence(t *testing.T) {
	reportHash := sha256.Sum256([]byte("hash me\n"))
	tests := []struct {
		name      string
		validator string
		broker    sessionBroker
	}{
		{
			name: "required output existence",
			validator: `      - type: outputExists
        path: "outputs/report.txt"
`,
			broker: outputWritingBroker{files: map[string]string{"outputs/report.txt": "exists\n"}},
		},
		{
			name: "json path equality",
			validator: `      - type: jsonEquals
        path: "outputs/metrics.json"
        jsonPath: "$.plugin.loaded"
        equals: true
`,
			broker: outputWritingBroker{files: map[string]string{"outputs/metrics.json": `{"plugin":{"loaded":true}}` + "\n"}},
		},
		{
			name: "json path equality normalizes nested numbers",
			validator: `      - type: jsonEquals
        path: "outputs/metrics.json"
        jsonPath: "$.mesh"
        equals:
          bounds: [1, 2, 3]
          vertexCount: 4
`,
			broker: outputWritingBroker{files: map[string]string{"outputs/metrics.json": `{"mesh":{"bounds":[1,2,3],"vertexCount":4}}` + "\n"}},
		},
		{
			name: "numeric approximate equality",
			validator: `      - type: numericApprox
        path: "outputs/metrics.json"
        jsonPath: "$.timings.solveMs"
        equals: 12.5
        tolerance: 0.1
`,
			broker: outputWritingBroker{files: map[string]string{"outputs/metrics.json": `{"timings":{"solveMs":12.45}}` + "\n"}},
		},
		{
			name: "numeric array approximate equality",
			validator: `      - type: numericApprox
        path: "outputs/metrics.json"
        jsonPath: "$.mesh.bounds"
        equals: [1.0, 2.0, 3.0]
        tolerance: 0.1
`,
			broker: outputWritingBroker{files: map[string]string{"outputs/metrics.json": `{"mesh":{"bounds":[1.01,1.99,3.05]}}` + "\n"}},
		},
		{
			name: "file hash",
			validator: fmt.Sprintf(`      - type: fileHash
        path: "outputs/report.txt"
        sha256: "%x"
`, reportHash),
			broker: outputWritingBroker{files: map[string]string{"outputs/report.txt": "hash me\n"}},
		},
		{
			name: "visual evidence",
			validator: `      - type: visualEvidence
        required: true
`,
			broker: outputWritingBroker{visualEvidence: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
`+tt.validator)
			var stdout, stderr bytes.Buffer
			runtime := defaultRunRuntime()
			runtime.Broker = tt.broker

			code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtime)
			if code != 0 {
				t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
			}
			evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
			bundle := readEvidenceBundle(t, evidence)
			if bundle.Status != "passed" {
				t.Fatalf("evidence status = %q, want passed", bundle.Status)
			}
			if len(bundle.Validators) != 1 || bundle.Validators[0].Status != "passed" {
				t.Fatalf("validator metadata = %+v", bundle.Validators)
			}
		})
	}
}

func TestRunScenarioRequiredOutputValidatorReportsMissingPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: outputExists
        path: "outputs/missing.txt"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if bundle.Status != "failed" {
		t.Fatalf("evidence status = %q, want failed", bundle.Status)
	}
	if len(bundle.Validators) != 1 || bundle.Validators[0].Status != "failed" || !strings.Contains(bundle.Validators[0].Message, "missing") {
		t.Fatalf("validator metadata = %+v", bundle.Validators)
	}
}

func TestRunScenarioValidatorsRejectSymlinkedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not consistently available on Windows test runners")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.json")
	mustWriteFile(t, outside, `{"token":"outside"}`+"\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: jsonEquals
        path: "outputs/secret.json"
        jsonPath: "$.token"
        equals: outside
`)
	var stdout, stderr bytes.Buffer
	runtimeConfig := defaultRunRuntime()
	runtimeConfig.Broker = symlinkOutputBroker{linkPath: "outputs/secret.json", target: outside}

	code := RunWithRuntime([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version", runtimeConfig)
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.Validators) != 1 || bundle.Validators[0].Status != "failed" || !strings.Contains(bundle.Validators[0].Message, "symlink") {
		t.Fatalf("validator metadata = %+v", bundle.Validators)
	}
	if _, err := os.Stat(filepath.Join(evidence, "outputs", "secret.json")); !os.IsNotExist(err) {
		t.Fatalf("symlinked output was copied into Evidence Bundle, stat err = %v", err)
	}
}

func TestRunScenarioSkipsOutputPathThatCollidesWithEvidenceMetadata(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "scenario-result.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.Outputs) != 0 {
		t.Fatalf("metadata-colliding Scenario Result was recorded as output: %+v", bundle.Outputs)
	}
	if _, err := os.Stat(filepath.Join(evidence, "scenario-result.json")); err != nil {
		t.Fatalf("Scenario Result metadata missing: %v", err)
	}
}

func TestRunScenarioInvalidValidatorPathStillWritesEvidence(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    validators:
      - type: outputExists
        path: "../secret.json"
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.Validators) != 1 || bundle.Validators[0].Status != "failed" || !strings.Contains(bundle.Validators[0].Message, "repo-relative") {
		t.Fatalf("validator metadata = %+v", bundle.Validators)
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
        enabled: false
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

type lockCorruptingVisualBroker struct{}

func (lockCorruptingVisualBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	return ScenarioResult{Status: resultStatusPassed}, nil
}

func (lockCorruptingVisualBroker) CaptureScreenshot(context runContext, request screenshotRequest) (visualEvidenceArtifact, error) {
	lockPath := filepath.Join(context.RepoDir, ".maya-stall", "state", "locks", "hosts", "fake-local.lock")
	if err := os.Remove(lockPath); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := os.Mkdir(lockPath, 0o755); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := os.WriteFile(filepath.Join(lockPath, "child"), []byte("blocks remove\n"), 0o644); err != nil {
		return visualEvidenceArtifact{}, err
	}
	return fakeSessionBroker{}.CaptureScreenshot(context, request)
}

type outputWritingBroker struct {
	files          map[string]string
	visualEvidence bool
}

type environmentCheckingBroker struct {
	runID       string
	resultPath  string
	environment map[string]string
}

func (broker *environmentCheckingBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	broker.runID = filepath.Base(context.StateDir)
	broker.resultPath = context.ScenarioResultPath
	broker.environment = context.Environment
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("checked run environment\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusPassed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "checked run environment"}, nil
}

type scenarioResultFileBroker struct{}

func (scenarioResultFileBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("script wrote scenario result\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	result := map[string]any{
		"status":      resultStatusFailed,
		"summary":     "script wrote result",
		"largeNumber": json.Number("9007199254740993"),
		"assertions": []map[string]any{
			{"name": "plugin loaded", "passed": false},
		},
	}
	if err := writeJSONFile(context.ScenarioResultPath, result); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusFailed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "broker fallback"}, nil
}

type partialScenarioResultFileBroker struct{}

func (partialScenarioResultFileBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("script wrote partial scenario result\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := writeJSONFile(context.ScenarioResultPath, map[string]any{"summary": "script partial result"}); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusFailed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusFailed, Summary: "broker failure"}, nil
}

type trailingScenarioResultBroker struct{}

func (trailingScenarioResultBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("script wrote malformed scenario result\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	content := []byte(`{"status":"passed"}{"status":"failed"}`)
	if err := os.MkdirAll(filepath.Dir(context.ScenarioResultPath), 0o755); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.ScenarioResultPath, content, 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusPassed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "broker fallback"}, nil
}

func (broker outputWritingBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("output-writing fake Session Broker completed\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	for relativePath, content := range broker.files {
		path := filepath.Join(context.Workspace, relativePath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ScenarioResult{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return ScenarioResult{}, err
		}
	}
	if broker.visualEvidence {
		path := filepath.Join(context.EvidenceDir, "screenshots", "smoke.png")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ScenarioResult{}, err
		}
		if err := os.WriteFile(path, []byte("fake png\n"), 0o644); err != nil {
			return ScenarioResult{}, err
		}
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusPassed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "outputs written"}, nil
}

type symlinkOutputBroker struct {
	linkPath string
	target   string
}

func (broker symlinkOutputBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.WriteFile(context.LogPath, []byte("symlink fake Session Broker completed\n"), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	linkPath := filepath.Join(context.Workspace, broker.linkPath)
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		return ScenarioResult{}, err
	}
	if err := os.Symlink(broker.target, linkPath); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusPassed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "symlink output written"}, nil
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

func writeFakeCommand(t *testing.T, dir string, name string, logPath string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake command fixtures need /bin/sh")
	}
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$0 $*\" >> %s\nexit %d\n", shellQuote(logPath), exitCode)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake command: %v", err)
	}
	return path
}

func writeFakeSFTPCommand(t *testing.T, dir string, logPath string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake sftp fixtures need /bin/sh")
	}
	path := filepath.Join(dir, "fake-sftp")
	content := fmt.Sprintf(`#!/bin/sh
while IFS= read -r line; do
  printf '%%s\n' "$line" >> %s
  case "$line" in
    *get*)
      local_path=${line##*\" \"}
      local_path=${local_path%%\"}
      local_path=${local_path##\"}
      mkdir -p "$(dirname "$local_path")"
      printf '{"status":"passed","summary":"downloaded by fake sftp"}\n' > "$local_path"
      ;;
  esac
done
exit 0
`, shellQuote(logPath))
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake sftp command: %v", err)
	}
	return path
}

func writeFailingGetSFTPCommand(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake sftp fixtures need /bin/sh")
	}
	path := filepath.Join(dir, "fake-sftp-failing-get")
	content := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    get*) exit 1 ;;
  esac
done
exit 0
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write failing fake sftp command: %v", err)
	}
	return path
}

func writeFailingCommandWithStderr(t *testing.T, dir string, name string, stderr string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake command fixtures need /bin/sh")
	}
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %s >&2\nexit 1\n", shellQuote(stderr))
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write failing fake command: %v", err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
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

func readEvidenceBundle(t *testing.T, evidenceDir string) evidenceBundle {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(evidenceDir, "evidence.json"))
	if err != nil {
		t.Fatalf("read evidence bundle: %v", err)
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		t.Fatalf("parse evidence bundle: %v", err)
	}
	return bundle
}

func publishedManifestHasURL(manifest publishedArtifactManifest, label string, url string) bool {
	for _, artifact := range manifest.Artifacts {
		if artifact.Label == label && artifact.URL == url {
			return true
		}
	}
	return false
}

func runIDFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		value, ok := strings.CutPrefix(line, "run: ")
		if ok {
			return strings.TrimSpace(value)
		}
	}
	t.Fatalf("run output missing run id:\n%s", output)
	return ""
}
