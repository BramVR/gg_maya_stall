package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const smokeHostConfigEnv = "MAYA_STALL_SMOKE_HOST_CONFIG"
const smokeTargetProfileEnv = "MAYA_STALL_SMOKE_TARGET_PROFILE"
const smokeHostEnv = "MAYA_STALL_SMOKE_HOST"
const consumingRepoSmokeDirEnv = "MAYA_STALL_CONSUMING_REPO_SMOKE_DIR"

func TestOptInRealSSHDoctorSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	dir := writeRunConfigFixture(t)
	restoreLiveSessionBrokerFixtures(t, options)
	report := runDoctor(dir, options.doctorOptions())
	assertLiveHostHealthProof(t, report)
	t.Logf("Host Health: %s", formatHostHealthReport(report))
	var stdout, stderr bytes.Buffer

	code := Run(options.doctorArgs(), &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("real SSH smoke doctor exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestOptInRealSSHConsumingRepoSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	restoreLiveSessionBrokerFixtures(t, options)
	runKLVPushConsumingRepoSmoke(t, options)
}

func TestOptInRealSSHRunSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	dir := writeLiveRunConfigFixture(t)
	restoreLiveSessionBrokerFixtures(t, options)

	doctorOptions := options.doctorOptions()
	doctorOptions.ScenarioName = "smoke"
	report := runDoctor(dir, doctorOptions)
	assertLiveHostHealthProof(t, report)
	t.Logf("Host Health: %s", formatHostHealthReport(report))

	var runStdout, runStderr bytes.Buffer
	runCode := Run(options.runArgs("smoke"), &runStdout, &runStderr, dir, "test-version")
	if runCode != 0 {
		t.Fatalf("real SSH smoke run exit code = %d, want 0; stdout: %s stderr: %s", runCode, runStdout.String(), runStderr.String())
	}
	evidenceDir := smokeOutputValue(runStdout.String(), "evidence")
	if evidenceDir == "" {
		t.Fatalf("real SSH smoke run did not print Evidence Bundle path:\n%s", runStdout.String())
	}
	bundle := assertLiveSmokeEvidenceBundle(t, evidenceDir)
	if bundle.Scenario != "smoke" {
		t.Fatalf("Evidence Bundle scenario = %q, want smoke", bundle.Scenario)
	}
	options.Host = bundle.Host
	host := liveSmokeHostConfigByID(t, options, bundle.Host)
	requireLiveScenarioRecordingArtifact(t, evidenceDir, bundle)

	var keptStdout, keptStderr bytes.Buffer
	keptCode := Run(options.runArgs("retention-failure", "--keep-on-failure"), &keptStdout, &keptStderr, dir, "test-version")
	if keptCode != 1 {
		t.Fatalf("real SSH retention run exit code = %d, want 1; stdout: %s stderr: %s", keptCode, keptStdout.String(), keptStderr.String())
	}
	runID := smokeOutputValue(keptStdout.String(), "run")
	if runID == "" || !strings.Contains(keptStdout.String(), "stopPolicy: kept") {
		t.Fatalf("real SSH retention run did not keep failed run:\nstdout: %s\nstderr: %s", keptStdout.String(), keptStderr.String())
	}

	var statusStdout, statusStderr bytes.Buffer
	statusCode := Run([]string{"status", "--run", runID}, &statusStdout, &statusStderr, dir, "test-version")
	if statusCode != 0 {
		t.Fatalf("real SSH retention status exit code = %d, want 0; stdout: %s stderr: %s", statusCode, statusStdout.String(), statusStderr.String())
	}
	if !strings.Contains(statusStdout.String(), "state: kept") || !strings.Contains(statusStdout.String(), "brokerSession:") {
		t.Fatalf("real SSH retention status missing kept broker session:\n%s", statusStdout.String())
	}

	var attachStdout, attachStderr bytes.Buffer
	attachCode := Run([]string{"attach", runID}, &attachStdout, &attachStderr, dir, "test-version")
	if attachCode != 0 {
		t.Fatalf("real SSH retention attach exit code = %d, want 0; stdout: %s stderr: %s", attachCode, attachStdout.String(), attachStderr.String())
	}
	if !strings.Contains(attachStdout.String(), "brokerReport:") || !strings.Contains(attachStdout.String(), "intentional retention smoke failure") {
		t.Fatalf("real SSH retention attach missing broker/local evidence:\n%s", attachStdout.String())
	}

	var stopStdout, stopStderr bytes.Buffer
	stopCode := Run([]string{"stop", runID}, &stopStdout, &stopStderr, dir, "test-version")
	if stopCode != 0 {
		t.Fatalf("real SSH retention stop exit code = %d, want 0; stdout: %s stderr: %s", stopCode, stopStdout.String(), stopStderr.String())
	}
	if !strings.Contains(stopStdout.String(), "stopped: "+runID) {
		t.Fatalf("real SSH retention stop missing run id:\n%s", stopStdout.String())
	}
	restoreLiveSessionBrokerFixture(t, host)
}

func runKLVPushConsumingRepoSmoke(t *testing.T, options realSSHSmokeOptions) {
	t.Helper()
	consumingRepoDir := os.Getenv(consumingRepoSmokeDirEnv)
	if consumingRepoDir == "" {
		t.Fatalf("%s is not set; live proof requires a real consuming repo checkout", consumingRepoSmokeDirEnv)
	}
	dir := writeKLVPushConsumingRepoSmokeFixture(t, consumingRepoDir)

	doctorOptions := options.doctorOptions()
	doctorOptions.ScenarioName = "klv-push-smoke"
	report := runDoctor(dir, doctorOptions)
	assertLiveHostHealthProof(t, report)
	t.Logf("Host Health: %s", formatHostHealthReport(report))

	var runStdout, runStderr bytes.Buffer
	runCode := Run(options.runArgs("klv-push-smoke"), &runStdout, &runStderr, dir, "test-version")
	if runCode != 0 {
		t.Fatalf("real consuming repo smoke run exit code = %d, want 0; stdout: %s stderr: %s", runCode, runStdout.String(), runStderr.String())
	}
	evidenceDir := smokeOutputValue(runStdout.String(), "evidence")
	if evidenceDir == "" {
		t.Fatalf("real consuming repo smoke did not print Evidence Bundle path:\n%s", runStdout.String())
	}
	bundle := assertLiveSmokeEvidenceBundle(t, evidenceDir)
	if bundle.Scenario != "klv-push-smoke" {
		t.Fatalf("Evidence Bundle scenario = %q, want klv-push-smoke", bundle.Scenario)
	}
	assertKLVPushScenarioResult(t, evidenceDir, bundle.ScenarioResult)

	storeDir := filepath.Join(t.TempDir(), "evidence-store")
	var publishStdout, publishStderr bytes.Buffer
	publishCode := Run([]string{
		"evidence", "publish",
		"--destination", storeDir,
		"--base-url", "https://evidence.example.invalid/maya-stall",
		evidenceDir,
	}, &publishStdout, &publishStderr, dir, "test-version")
	if publishCode != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", publishCode, publishStdout.String(), publishStderr.String())
	}
	for _, name := range []string{"artifact-manifest.json", "review-comment.md"} {
		if _, err := os.Stat(filepath.Join(storeDir, bundle.RunID, name)); err != nil {
			t.Fatalf("published Evidence Store missing %s: %v", name, err)
		}
	}
	if !strings.Contains(publishStdout.String(), "url: https://evidence.example.invalid/maya-stall/") {
		t.Fatalf("evidence publish did not print review-ready URL:\n%s", publishStdout.String())
	}
}

func TestKLVPushConsumingRepoSmokeFixtureUsesBuiltPluginArtifact(t *testing.T) {
	source := t.TempDir()
	mustWriteFile(t, filepath.Join(source, "pyproject.toml"), "[project]\nname = \"gg-klv-push\"\n")
	mustWriteFile(t, filepath.Join(source, "packages", "klv_push", "README.md"), "# KLV Push\n")
	mustWriteFile(t, filepath.Join(source, "packages", "klv_push", "pyproject.toml"), "[project]\nname = \"klv-push\"\n")
	mustWriteFile(t, filepath.Join(source, "src", "klv_push", "__init__.py"), "\"\"\"test\"\"\"\n")
	mustWriteFile(t, filepath.Join(source, "src", "klv_push", "klvPush.py"), "NODE_NAME = 'klvPush'\n")

	dir := writeKLVPushConsumingRepoSmokeFixture(t, source)
	config, _, err := loadRepoRunConfig(dir)
	if err != nil {
		t.Fatalf("load generated Repo Run Config: %v", err)
	}
	scenario := config.Scenarios["klv-push-smoke"]
	if len(scenario.Payload.PluginArtifacts) != 1 || scenario.Payload.PluginArtifacts[0] != "packages/klv_push" {
		t.Fatalf("pluginArtifacts = %#v, want packages/klv_push", scenario.Payload.PluginArtifacts)
	}
	if len(scenario.Payload.IncludePaths) != 1 || scenario.Payload.IncludePaths[0] != "packages/klv_push/scripts" {
		t.Fatalf("includePaths = %#v, want built package scripts", scenario.Payload.IncludePaths)
	}
	if _, err := os.Stat(filepath.Join(dir, "packages", "klv_push", "scripts", "klv_push", "klvPush.py")); err != nil {
		t.Fatalf("built Plugin Artifact missing klvPush.py: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "maya", "klv_push_smoke.py")); err != nil {
		t.Fatalf("Maya Script missing: %v", err)
	}
}

type realSSHSmokeOptions struct {
	HostConfig    string
	TargetProfile string
	Host          string
}

func realSSHSmokeOptionsFromEnv(t *testing.T) (realSSHSmokeOptions, bool) {
	t.Helper()
	hostConfig, ok := os.LookupEnv(smokeHostConfigEnv)
	if !ok || hostConfig == "" {
		t.Skip(smokeHostConfigEnv + " is not set; skipping opt-in real SSH smoke")
		return realSSHSmokeOptions{}, false
	}
	options := realSSHSmokeOptions{HostConfig: hostConfig, TargetProfile: "default"}
	if value, ok := os.LookupEnv(smokeTargetProfileEnv); ok && value != "" {
		options.TargetProfile = value
	}
	if value, ok := os.LookupEnv(smokeHostEnv); ok && value != "" {
		options.Host = value
	}
	return options, true
}

func (options realSSHSmokeOptions) doctorArgs(extra ...string) []string {
	args := []string{"doctor", "--host-config", options.HostConfig, "--target-profile", options.TargetProfile}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	return append(args, extra...)
}

func (options realSSHSmokeOptions) doctorOptions() doctorOptions {
	return doctorOptions{HostConfig: options.HostConfig, TargetProfile: options.TargetProfile, HostPin: options.Host}
}

func (options realSSHSmokeOptions) runArgs(scenario string, extra ...string) []string {
	args := []string{"run", "--host-config", options.HostConfig, "--target-profile", options.TargetProfile}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	args = append(args, extra...)
	return append(args, scenario)
}

func (options realSSHSmokeOptions) recordArgs(extra ...string) []string {
	args := []string{"record", "--host-config", options.HostConfig, "--target-profile", options.TargetProfile}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	return append(args, extra...)
}

func (options realSSHSmokeOptions) screenshotArgs(extra ...string) []string {
	args := []string{"screenshot", "--host-config", options.HostConfig, "--target-profile", options.TargetProfile}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	return append(args, extra...)
}

func (options realSSHSmokeOptions) controlClickArgs(x int, y int, extra ...string) []string {
	args := []string{
		"control", "click",
		"--host-config", options.HostConfig,
		"--target-profile", options.TargetProfile,
	}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	args = append(args, "--x", strconv.Itoa(x), "--y", strconv.Itoa(y))
	return append(args, extra...)
}

func assertLiveHostHealthProof(t *testing.T, report hostHealthReport) {
	t.Helper()
	if !report.Healthy {
		t.Fatalf("Host Health is not healthy: %s", formatHostHealthReport(report))
	}
	if report.Runtime.Profile != "ssh-sessiond" || report.Runtime.HostAdapter != "ssh" || report.Runtime.BrokerAdapter != "gg-mayasessiond" || !report.Runtime.LiveProofEligible {
		t.Fatalf("Host Health runtime = %+v, want live-proof-eligible ssh-sessiond", report.Runtime)
	}
	broker := requireHostHealthLayer(t, report, "session-broker")
	if broker.Status != "ok" || broker.Source != "gg-mayasessiond" || !broker.InteractiveDesktop {
		t.Fatalf("session-broker Host Health = %+v, want interactive gg_mayasessiond", broker)
	}
	visual := requireHostHealthLayer(t, report, "visual-evidence")
	if visual.Status != "ok" || visual.Source != "session-broker" || !strings.Contains(visual.Detail, "viewport.capture") {
		t.Fatalf("visual-evidence Host Health = %+v, want broker-backed viewport.capture", visual)
	}
}

func writeLiveRunConfigFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('maya-stall live smoke')\n")
	mustWriteFile(t, filepath.Join(dir, "maya", "retention_failure.py"), "raise RuntimeError('intentional retention smoke failure')\n")
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    description: "Live Maya Host smoke Scenario."
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/smoke.py"
    expectedOutputs:
      files: []
      scenarioResult: "outputs/smoke-result.json"
    evidence:
      screenshots:
        enabled: true
      recording:
        enabled: true
    validators:
      - type: scenarioResultStatus
        status: passed
      - type: visualEvidence
  retention-failure:
    description: "Live Maya Host retention failure Scenario."
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/retention_failure.py"
    expectedOutputs:
      files: []
      scenarioResult: "outputs/retention-result.json"
    evidence:
      screenshots:
        enabled: false
    validators:
      - type: scenarioResultStatus
        status: passed
`)
	return dir
}

func smokeOutputValue(output string, key string) string {
	prefix := key + ": "
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func assertLiveSmokeEvidenceBundle(t *testing.T, evidenceDir string) evidenceBundle {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(evidenceDir, "evidence.json"))
	if err != nil {
		t.Fatalf("read Evidence Bundle: %v", err)
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		t.Fatalf("parse Evidence Bundle: %v", err)
	}
	if bundle.Status != resultStatusPassed {
		t.Fatalf("Evidence Bundle status = %q, want %q", bundle.Status, resultStatusPassed)
	}
	if bundle.Runtime.Profile != "ssh-sessiond" || bundle.Runtime.HostAdapter != "ssh" || bundle.Runtime.BrokerAdapter != "gg-mayasessiond" || !bundle.Runtime.LiveProofEligible {
		t.Fatalf("Evidence Bundle runtime = %+v, want live-proof-eligible ssh-sessiond", bundle.Runtime)
	}
	if len(bundle.VisualEvidence) == 0 {
		t.Fatalf("Evidence Bundle missing Visual Evidence")
	}
	for _, relative := range []string{bundle.Manifest, bundle.Events, bundle.Log, bundle.ScenarioResult} {
		if relative == "" {
			t.Fatalf("Evidence Bundle has empty required path")
		}
		if _, err := os.Stat(filepath.Join(evidenceDir, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("Evidence Bundle missing %s: %v", relative, err)
		}
	}
	for _, artifact := range bundle.VisualEvidence {
		if artifact.Kind == "" || artifact.Path == "" || artifact.MediaType == "" {
			t.Fatalf("Visual Evidence artifact incomplete: %+v", artifact)
		}
		if artifact.TargetProfile != bundle.TargetProfile || artifact.Host != bundle.Host {
			t.Fatalf("Visual Evidence target metadata = %+v, want Target Profile %q and Maya Host %q", artifact, bundle.TargetProfile, bundle.Host)
		}
		visualPath := filepath.Join(evidenceDir, filepath.FromSlash(artifact.Path))
		content, err := os.ReadFile(visualPath)
		if err != nil {
			t.Fatalf("Visual Evidence artifact missing %s: %v", artifact.Path, err)
		}
		if len(content) == 0 {
			t.Fatalf("Visual Evidence artifact %s is empty", artifact.Path)
		}
		switch artifact.Kind {
		case "recording":
			if artifact.MediaType != "video/mp4" {
				t.Fatalf("recording Visual Evidence media type = %q, want video/mp4", artifact.MediaType)
			}
			if artifact.DurationSeconds <= 0 || artifact.FPS <= 0 {
				t.Fatalf("recording Visual Evidence missing duration/FPS metadata: %+v", artifact)
			}
			if !looksLikeMP4Bytes(content) {
				t.Fatalf("recording Visual Evidence artifact %s does not look like an MP4", artifact.Path)
			}
		default:
			if !looksLikeImageBytes(artifact.MediaType, content) {
				t.Fatalf("Visual Evidence artifact %s does not match media type %s", artifact.Path, artifact.MediaType)
			}
		}
	}
	return bundle
}

func requireLiveScenarioRecordingArtifact(t *testing.T, evidenceDir string, bundle evidenceBundle) visualEvidenceArtifact {
	t.Helper()
	for _, artifact := range bundle.VisualEvidence {
		if artifact.Kind != "recording" {
			continue
		}
		if artifact.MediaType != "video/mp4" || !strings.HasPrefix(cleanEvidenceArtifactPath(artifact.Path), evidenceRecordingsDir+"/") {
			t.Fatalf("Scenario recording artifact = %+v, want video/mp4 under recordings/", artifact)
		}
		if artifact.DurationSeconds <= 0 || artifact.FPS <= 0 {
			t.Fatalf("Scenario recording missing duration/FPS metadata: %+v", artifact)
		}
		if artifact.TargetProfile != bundle.TargetProfile || artifact.Host != bundle.Host {
			t.Fatalf("Scenario recording target metadata = %+v, want Target Profile %q and Maya Host %q", artifact, bundle.TargetProfile, bundle.Host)
		}
		content, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(artifact.Path)))
		if err != nil {
			t.Fatalf("read Scenario recording %s: %v", artifact.Path, err)
		}
		if !looksLikeMP4Bytes(content) {
			t.Fatalf("Scenario recording %s does not look like an MP4", artifact.Path)
		}
		return artifact
	}
	t.Fatalf("Evidence Bundle missing Scenario recording Visual Evidence: %+v", bundle.VisualEvidence)
	return visualEvidenceArtifact{}
}

func writeKLVPushConsumingRepoSmokeFixture(t *testing.T, sourceRepoDir string) string {
	t.Helper()
	sourceRepoDir, err := filepath.Abs(sourceRepoDir)
	if err != nil {
		t.Fatalf("resolve consuming repo path: %v", err)
	}
	for _, required := range []string{
		"pyproject.toml",
		"packages/klv_push",
		"src/klv_push/__init__.py",
		"src/klv_push/klvPush.py",
	} {
		if _, err := os.Stat(filepath.Join(sourceRepoDir, filepath.FromSlash(required))); err != nil {
			t.Fatalf("consuming repo missing %s: %v", required, err)
		}
	}

	dir := t.TempDir()
	if err := copyPath(filepath.Join(sourceRepoDir, "packages", "klv_push"), filepath.Join(dir, "packages", "klv_push")); err != nil {
		t.Fatalf("copy klv_push package artifact shell: %v", err)
	}
	if err := copyPath(filepath.Join(sourceRepoDir, "src", "klv_push"), filepath.Join(dir, "packages", "klv_push", "scripts", "klv_push")); err != nil {
		t.Fatalf("build klv_push Plugin Artifact: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "pyproject.toml"), "[project]\nname = \"klv-push-consuming-smoke\"\n")
	mustWriteFile(t, filepath.Join(dir, "maya", "klv_push_smoke.py"), klvPushConsumingRepoSmokeScript())
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  klv-push-smoke:
    description: "KLV Push consuming repo smoke Scenario."
    mayaVersion: "2025"
    payload:
      scripts:
        - "maya/klv_push_smoke.py"
      pluginArtifacts:
        - "packages/klv_push"
      includePaths:
        - "packages/klv_push/scripts"
    expectedOutputs:
      files:
        - "outputs/klv-push-smoke-result.json"
      scenarioResult: "outputs/klv-push-smoke-result.json"
    evidence:
      screenshots:
        enabled: true
    validators:
      - type: scenarioResultStatus
        status: passed
      - type: visualEvidence
      - type: jsonEquals
        path: "outputs/klv-push-smoke-result.json"
        jsonPath: "$.pluginName"
        equals: "klv_push"
      - type: jsonEquals
        path: "outputs/klv-push-smoke-result.json"
        jsonPath: "$.import.ok"
        equals: true
      - type: jsonEquals
        path: "outputs/klv-push-smoke-result.json"
        jsonPath: "$.action.ok"
        equals: true
`)
	return dir
}

func klvPushConsumingRepoSmokeScript() string {
	return `import json
import os
import sys
import traceback

import maya.cmds as cmds

result_path = os.environ["MAYA_STALL_SCENARIO_RESULT"]
artifact_root = os.path.abspath(os.path.join(os.getcwd(), "..", "payload", "pluginArtifacts", "packages", "klv_push"))
package_scripts = os.path.join(artifact_root, "scripts")

def write_result(status, summary, **fields):
    payload = {"status": status, "summary": summary}
    payload.update(fields)
    parent = os.path.dirname(result_path)
    if parent:
        os.makedirs(parent, exist_ok=True)
    with open(result_path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, indent=2, sort_keys=True)
        handle.write("\n")

try:
    for module_name in list(sys.modules):
        if module_name == "klv_push" or module_name.startswith("klv_push."):
            sys.modules.pop(module_name, None)
    sys.path.insert(0, package_scripts)
    import klv_push.klvPush as klv_push_plugin

    imported_path = os.path.normcase(os.path.abspath(getattr(klv_push_plugin, "__file__", "")))
    package_root = os.path.normcase(os.path.abspath(package_scripts))
    if not imported_path.startswith(package_root + os.sep):
        raise RuntimeError("klv_push imported outside staged Plugin Artifact")

    cmds.file(new=True, force=True)
    marker = cmds.polyCube(name="mayaStallKlvPushImportMarker", width=1, height=1, depth=1)[0]
    cmds.select(marker, replace=True)
    cmds.refresh(force=True)
    write_result(
        "passed",
        "klv_push plugin module imported from the staged artifact and a Maya marker object was created.",
        pluginName="klv_push",
        action={"ok": True, "createdNodes": [marker], "nodeType": getattr(klv_push_plugin, "NODE_NAME", "klvPush")},
        mayaVersion=str(cmds.about(version=True)),
        artifact={"name": "packages/klv_push", "builtScriptsPackage": True},
        **{"import": {"ok": True, "module": getattr(klv_push_plugin, "__name__", "klv_push.klvPush"), "fromStagedArtifact": True}},
    )
except Exception as exc:
    write_result(
        "failed",
        str(exc),
        pluginName="klv_push",
        action={"ok": False, "createdNodes": []},
        mayaVersion=str(cmds.about(version=True)) if "cmds" in globals() else "",
        traceback=traceback.format_exc(),
        **{"import": {"ok": False}},
    )
    raise
`
}

func assertKLVPushScenarioResult(t *testing.T, evidenceDir string, relativeResultPath string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(relativeResultPath)))
	if err != nil {
		t.Fatalf("read KLV Push Scenario Result: %v", err)
	}
	var result struct {
		Status     string `json:"status"`
		PluginName string `json:"pluginName"`
		Import     struct {
			OK bool `json:"ok"`
		} `json:"import"`
		Action struct {
			OK           bool     `json:"ok"`
			CreatedNodes []string `json:"createdNodes"`
		} `json:"action"`
		MayaVersion string `json:"mayaVersion"`
	}
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatalf("parse KLV Push Scenario Result: %v", err)
	}
	if result.Status != resultStatusPassed || result.PluginName != "klv_push" || !result.Import.OK || !result.Action.OK || result.MayaVersion == "" || len(result.Action.CreatedNodes) == 0 {
		t.Fatalf("KLV Push Scenario Result missing product proof: %+v", result)
	}
}

func looksLikeImageBytes(mediaType string, content []byte) bool {
	switch mediaType {
	case "image/jpeg":
		return len(content) >= 3 && content[0] == 0xff && content[1] == 0xd8 && content[2] == 0xff
	case "image/png":
		return len(content) >= 8 &&
			content[0] == 0x89 &&
			content[1] == 'P' &&
			content[2] == 'N' &&
			content[3] == 'G' &&
			content[4] == '\r' &&
			content[5] == '\n' &&
			content[6] == 0x1a &&
			content[7] == '\n'
	default:
		return len(content) > 0
	}
}
