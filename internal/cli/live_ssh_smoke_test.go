package cli

import (
	"bytes"
	"os"
	"testing"
)

const smokeHostConfigEnv = "MAYA_STALL_SMOKE_HOST_CONFIG"
const smokeTargetProfileEnv = "MAYA_STALL_SMOKE_TARGET_PROFILE"
const smokeHostEnv = "MAYA_STALL_SMOKE_HOST"

func TestOptInRealSSHDoctorSmoke(t *testing.T) {
	hostConfig, ok := os.LookupEnv(smokeHostConfigEnv)
	if !ok || hostConfig == "" {
		t.Skip(smokeHostConfigEnv + " is not set; skipping opt-in real SSH smoke")
	}
	targetProfile := "default"
	if value, ok := os.LookupEnv(smokeTargetProfileEnv); ok && value != "" {
		targetProfile = value
	}
	args := []string{"doctor", "--host-config", hostConfig, "--target-profile", targetProfile}
	if host, ok := os.LookupEnv(smokeHostEnv); ok && host != "" {
		args = append(args, "--host", host)
	}
	dir := writeRunConfigFixture(t)
	var stdout, stderr bytes.Buffer

	code := Run(args, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("real SSH smoke doctor exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
}
