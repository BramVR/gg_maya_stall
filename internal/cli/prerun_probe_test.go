package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunScenarioFailsClosedWhenConfiguredHealthyTransportIsUnreachable(t *testing.T) {
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
        ssh: unreachable
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"pre-run readiness failed at ssh layer for Maya Host alpha",
		`fake SSH transport is "unreachable"`,
		"docs/setup/windows-maya-host.md#openssh-reachability",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("run error missing %q:\n%s", want, stderr.String())
		}
	}
	requirePreRunReadinessFailureEvidence(t, dir, stdout.String(), "alpha")
}

func TestRunScenarioFailsClosedWhenSessionBrokerIsNotReady(t *testing.T) {
	dir := writeRunConfigFixture(t)
	sftpLog := filepath.Join(dir, "sftp.log")
	sftpPath := writeFakeSFTPCommand(t, dir, sftpLog)
	sshPath := writeSequencedFakeSSHCommand(t, dir, filepath.Join(dir, "ssh.log"), []string{
		`@prerun-explicit`,
		``,
		`{"ok":false,"error":"state dir missing"}`,
	})
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
        transport: ssh
        ssh:
          host: maya-win-01
          binary: `+strconv.Quote(sshPath)+`
          sftpBinary: `+strconv.Quote(sftpPath)+`
        workRoot: C:/maya-stall
        broker:
          type: gg-mayasessiond
          stateDir: C:/maya-stall/sessiond-ui
          python: C:/maya-stall/sessiond-venv311/Scripts/python.exe
          repo: C:/maya-stall/tools/GG_MayaSessiond
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"run", "--host-config", hostConfigPath, "--target-profile", "ci", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 1 {
		t.Fatalf("run exit code = %d, want 1; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"pre-run readiness failed at session-broker layer for Maya Host alpha",
		"gg_mayasessiond status failed: state dir missing",
		"docs/setup/windows-maya-host.md#session-broker",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("run error missing %q:\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(sftpLog); !os.IsNotExist(err) {
		t.Fatalf("readiness failure staged payload: %v", err)
	}
	sshBytes, err := os.ReadFile(filepath.Join(dir, "ssh.log"))
	if err != nil {
		t.Fatalf("read SSH log: %v", err)
	}
	if got := strings.Count(string(sshBytes), "CALL "); got != 2 {
		t.Fatalf("readiness check made %d SSH calls, want transport plus broker status:\n%s", got, sshBytes)
	}
	requirePreRunReadinessFailureEvidence(t, dir, stdout.String(), "alpha")
}

func TestSessionBrokerReadinessAcceptsStoppedStateForFreshRestart(t *testing.T) {
	dir := t.TempDir()
	sshPath := writeSequencedFakeSSHCommand(t, dir, filepath.Join(dir, "ssh.log"), []string{
		`@prerun-explicit`,
		`{"has_state":true,"derived_status":"stopped","state":{"status":"stopped","session_id":"previous-session"}}`,
	})
	broker := ggMayaSessiondBroker{host: mayaHostConfig{
		Transport: "ssh",
		SSH:       sshConfig{Host: "maya-win-01", Binary: sshPath},
		WorkRoot:  "C:/maya-stall",
		Broker: brokerConfig{
			Type:     "gg-mayasessiond",
			StateDir: "C:/maya-stall/sessiond-ui",
			Python:   "C:/maya-stall/sessiond-venv311/Scripts/python.exe",
			Repo:     "C:/maya-stall/tools/GG_MayaSessiond",
		},
	}}

	if err := broker.ProbeSessionBroker(preRunProbeLayerTimeout); err != nil {
		t.Fatalf("stopped Session Broker readiness error = %v, want restartable state accepted", err)
	}
}

func TestSessionBrokerReadinessRejectsNoncanonicalState(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "missing state", output: `{"has_state":false,"derived_status":"stopped","state":{}}`},
		{name: "inconsistent running state", output: `{"has_state":true,"derived_status":"running","state":{"status":"stopped"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			sshPath := writeSequencedFakeSSHCommand(t, dir, filepath.Join(dir, "ssh.log"), []string{
				`@prerun-explicit`,
				test.output,
			})
			broker := testSessiondReadinessBroker(sshPath)

			if err := broker.ProbeSessionBroker(preRunProbeLayerTimeout); err == nil {
				t.Fatal("noncanonical Session Broker state readiness error = nil, want failure")
			}
		})
	}
}

func TestSessionBrokerReadinessRejectsStatusTimeoutAfterJSON(t *testing.T) {
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "fake-ssh-timeout")
	mustWriteFile(t, sshPath, "#!/bin/sh\nprintf '%s\\n' '{\"has_state\":true,\"derived_status\":\"stopped\",\"state\":{\"status\":\"stopped\"}}'\nexec sleep 10\n")
	if err := os.Chmod(sshPath, 0o755); err != nil {
		t.Fatalf("chmod fake SSH command: %v", err)
	}
	broker := testSessiondReadinessBroker(sshPath)

	err := broker.ProbeSessionBroker(time.Second)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timed-out Session Broker readiness error = %v, want timeout failure", err)
	}
	status, err := broker.statusWithTimeout(time.Second)
	if err != nil || !strings.EqualFold(sessiondEffectiveStatus(status), "stopped") {
		t.Fatalf("legacy Session Broker status timeout recovery = %+v, %v; want stopped status", status, err)
	}
}

func testSessiondReadinessBroker(sshPath string) ggMayaSessiondBroker {
	return ggMayaSessiondBroker{host: mayaHostConfig{
		Transport: "ssh",
		SSH:       sshConfig{Host: "maya-win-01", Binary: sshPath},
		WorkRoot:  "C:/maya-stall",
		Broker: brokerConfig{
			Type:     "gg-mayasessiond",
			StateDir: "C:/maya-stall/sessiond-ui",
			Python:   "C:/maya-stall/sessiond-venv311/Scripts/python.exe",
			Repo:     "C:/maya-stall/tools/GG_MayaSessiond",
		},
	}}
}

func requirePreRunReadinessFailureEvidence(t *testing.T, dir string, output string, hostID string) {
	t.Helper()
	lockPath := filepath.Join(dir, ".maya-stall", "state", "locks", "hosts", hostID+".lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("pre-run readiness failure left Host Lock %s: %v", lockPath, err)
	}
	runID := outputValue(t, output, "run")
	stateDir := filepath.Join(dir, ".maya-stall", "state", "runs", runID)
	if _, err := os.Stat(stateDir); err != nil {
		t.Fatalf("pre-run readiness failure Run State missing: %v", err)
	}
	bundle := readEvidenceBundle(t, filepath.Join(dir, "artifacts", "maya-stall", runID))
	if bundle.Failure == nil || bundle.Failure.FailedLayer != "remote-check" || bundle.Failure.CleanupState != "completed" {
		t.Fatalf("pre-run readiness failure Evidence Bundle = %+v", bundle.Failure)
	}
}
