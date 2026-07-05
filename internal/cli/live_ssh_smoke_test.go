package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const smokeHostConfigEnv = "MAYA_STALL_SMOKE_HOST_CONFIG"
const smokeTargetProfileEnv = "MAYA_STALL_SMOKE_TARGET_PROFILE"
const smokeHostEnv = "MAYA_STALL_SMOKE_HOST"

func TestOptInRealSSHDoctorSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run(options.doctorArgs(), &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("real SSH smoke doctor exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestOptInRealSSHRunSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	dir := writeLiveRunConfigFixture(t)

	var doctorStdout, doctorStderr bytes.Buffer
	doctorCode := Run(options.doctorArgs("--scenario", "smoke"), &doctorStdout, &doctorStderr, dir, "test-version")
	if doctorCode != 0 {
		t.Fatalf("real SSH smoke doctor exit code = %d, want 0; stdout: %s stderr: %s", doctorCode, doctorStdout.String(), doctorStderr.String())
	}

	var runStdout, runStderr bytes.Buffer
	runCode := Run(options.runArgs("smoke"), &runStdout, &runStderr, dir, "test-version")
	if runCode != 0 {
		t.Fatalf("real SSH smoke run exit code = %d, want 0; stdout: %s stderr: %s", runCode, runStdout.String(), runStderr.String())
	}
	evidenceDir := smokeOutputValue(runStdout.String(), "evidence")
	if evidenceDir == "" {
		t.Fatalf("real SSH smoke run did not print Evidence Bundle path:\n%s", runStdout.String())
	}
	assertLiveSmokeEvidenceBundle(t, evidenceDir)
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

func (options realSSHSmokeOptions) runArgs(scenario string) []string {
	args := []string{"run", "--host-config", options.HostConfig, "--target-profile", options.TargetProfile}
	if options.Host != "" {
		args = append(args, "--host", options.Host)
	}
	return append(args, scenario)
}

func writeLiveRunConfigFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "maya", "smoke.py"), "print('maya-stall live smoke')\n")
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
        enabled: false
    validators:
      - type: scenarioResultStatus
        status: passed
      - type: visualEvidence
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

func assertLiveSmokeEvidenceBundle(t *testing.T, evidenceDir string) {
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
	if bundle.Scenario != "smoke" {
		t.Fatalf("Evidence Bundle scenario = %q, want smoke", bundle.Scenario)
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
		visualPath := filepath.Join(evidenceDir, filepath.FromSlash(artifact.Path))
		content, err := os.ReadFile(visualPath)
		if err != nil {
			t.Fatalf("Visual Evidence artifact missing %s: %v", artifact.Path, err)
		}
		if len(content) == 0 {
			t.Fatalf("Visual Evidence artifact %s is empty", artifact.Path)
		}
		if !looksLikeImageBytes(artifact.MediaType, content) {
			t.Fatalf("Visual Evidence artifact %s does not match media type %s", artifact.Path, artifact.MediaType)
		}
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
