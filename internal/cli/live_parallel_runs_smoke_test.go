package cli

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestOptInRealTwoHostParallelRunsSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	if options.Host != "" {
		t.Skipf("two-Host parallel smoke requires an unpinned Host Pool; unset %s", smokeHostEnv)
	}
	repoDir := writeLiveRunConfigFixture(t)
	hosts := compatibleLiveParallelSmokeHosts(t, options, repoDir)
	for _, host := range hosts {
		hostOptions := options
		hostOptions.Host = host.ID
		restoreLiveSessionBrokerFixtures(t, hostOptions)
	}

	dataDir := privateTempDir(t)
	handlerValue, err := newControlPlaneHandler(dataDir, "parallel-live-operator-token", defaultRunRuntime())
	if err != nil {
		t.Fatalf("create two-Host live Control Plane: %v", err)
	}
	handler := handlerValue.(*controlPlaneHandler)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	t.Setenv(defaultControlPlaneTokenEnv, "parallel-live-operator-token")
	t.Setenv("TEST_MAYA_STALL_HOST_AGENT_CREDENTIAL", testHostAgentCredential)
	runtime := defaultRunRuntime()
	runtime.ControlPlaneHTTPClient = server.Client()

	firstAgentID := "windows-agent-live-01"
	secondAgentID := "windows-agent-live-02"
	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, firstAgentID, hosts[0].ID)
	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, secondAgentID, hosts[1].ID)
	firstAgent := startBlockedParallelHostAgent(t, repoDir, server.URL, runtime, firstAgentID, hosts[0].ID, privateTempDir(t), options.HostConfig, nil)
	secondAgent := startBlockedParallelHostAgent(t, repoDir, server.URL, runtime, secondAgentID, hosts[1].ID, privateTempDir(t), options.HostConfig, nil)
	waitForHostAgentStateWithToken(t, server.Client(), server.URL, "parallel-live-operator-token", firstAgentID, "ready")
	waitForHostAgentStateWithToken(t, server.Client(), server.URL, "parallel-live-operator-token", secondAgentID, "ready")

	firstRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	firstAccepted := awaitParallelAcceptedWithin(t, firstRun, 30*time.Second)
	firstSession := awaitParallelSessionWithin(t, firstAgent, 5*time.Minute)
	secondRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	secondAccepted := awaitParallelAcceptedWithin(t, secondRun, 30*time.Second)
	_ = awaitParallelSessionWithin(t, secondAgent, 5*time.Minute)
	if firstAccepted.RunID == secondAccepted.RunID {
		t.Fatalf("parallel live Runs received one Run ID: %s", firstAccepted.RunID)
	}
	assertParallelAgentOwnsRunWithToken(t, server, handler, firstAgent, firstAccepted.RunID, "parallel-live-operator-token")
	assertParallelAgentOwnsRunWithToken(t, server, handler, secondAgent, secondAccepted.RunID, "parallel-live-operator-token")

	thirdRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	thirdAccepted := awaitParallelAcceptedWithin(t, thirdRun, 30*time.Second)
	var queued controlPlaneStatusResponse
	if err := getControlPlaneJSON(server.URL, "", "/v1/runs/"+thirdAccepted.RunID+"/status", runtime, &queued); err != nil {
		t.Fatalf("read live third Run queue status: %v", err)
	}
	if queued.State != "queued" || queued.QueuePosition != 1 || queued.WaitReason != "compatible-hosts-busy" || queued.Host != "" {
		t.Fatalf("live third Run queue status = %+v", queued)
	}
	assertParallelAgentOwnsRunWithToken(t, server, handler, firstAgent, firstAccepted.RunID, "parallel-live-operator-token")
	assertParallelAgentOwnsRunWithToken(t, server, handler, secondAgent, secondAccepted.RunID, "parallel-live-operator-token")

	firstAgent.release()
	awaitLiveParallelAgentExit(t, firstAgent, 10*time.Minute)
	awaitLiveParallelRunExit(t, firstRun, 2*time.Minute)
	waitForHostAgentStateWithToken(t, server.Client(), server.URL, "parallel-live-operator-token", firstAgentID, "offline")

	replacementAgent := startBlockedParallelHostAgent(t, repoDir, server.URL, runtime, firstAgentID, hosts[0].ID, firstAgent.workRoot, options.HostConfig, nil)
	thirdSession := awaitParallelSessionWithin(t, replacementAgent, 5*time.Minute)
	if thirdSession == firstSession {
		t.Fatalf("queued live Run reused the prior broker session on Host %s: %+v", hosts[0].ID, thirdSession)
	}
	assertParallelAgentOwnsRunWithToken(t, server, handler, replacementAgent, thirdAccepted.RunID, "parallel-live-operator-token")
	assertParallelAgentOwnsRunWithToken(t, server, handler, secondAgent, secondAccepted.RunID, "parallel-live-operator-token")

	secondAgent.release()
	replacementAgent.release()
	awaitLiveParallelAgentExit(t, secondAgent, 10*time.Minute)
	awaitLiveParallelAgentExit(t, replacementAgent, 10*time.Minute)
	awaitLiveParallelRunExit(t, secondRun, 2*time.Minute)
	awaitLiveParallelRunExit(t, thirdRun, 2*time.Minute)

	proofs := []liveParallelRunProof{
		{runID: firstAccepted.RunID, host: hosts[0], agentRoot: firstAgent.workRoot},
		{runID: secondAccepted.RunID, host: hosts[1], agentRoot: secondAgent.workRoot},
		{runID: thirdAccepted.RunID, host: hosts[0], agentRoot: replacementAgent.workRoot},
	}
	for _, proof := range proofs {
		assertLiveParallelRunIsolation(t, server.URL, runtime, handler, dataDir, options.TargetProfile, proof, proofs)
	}
	t.Log("two distinct real Maya Hosts overlapped with bound Host Locks; a third Run queued and dispatched after one Host freed; all three Evidence Bundles and cleanup paths remained Run-ID isolated")
}

type liveParallelRunProof struct {
	runID     string
	host      mayaHostConfig
	agentRoot string
}

func compatibleLiveParallelSmokeHosts(t *testing.T, options realSSHSmokeOptions, repoDir string) []mayaHostConfig {
	t.Helper()
	config, err := loadUserHostConfig(options.HostConfig)
	if err != nil {
		t.Fatalf("load two-Host live config: %v", err)
	}
	candidates, err := hostCandidates(config, options.TargetProfile, "")
	if err != nil {
		t.Fatalf("resolve two-Host live pool: %v", err)
	}
	requirements, err := scenarioRequirementsForScheduling(repoDir, "smoke")
	if err != nil {
		t.Fatalf("load live smoke Scenario requirements: %v", err)
	}
	compatible := make([]mayaHostConfig, 0, len(candidates))
	for _, host := range candidates {
		if !isHealthyHost(host) || !host.usesRealSSH() {
			continue
		}
		resolved, resolveErr := resolveLiveHostAgentRuntime(host)
		if resolveErr != nil || !resolved.Metadata.LiveProofEligible {
			continue
		}
		report, reportErr := hostAgentCapabilityRecord(hostAgentRunOnceOptions{HostID: host.ID, HostConfig: options.HostConfig}, time.Now())
		if reportErr != nil || !decideMayaHostCompatibility(requirements, report, time.Now()).Compatible {
			continue
		}
		compatible = append(compatible, host)
	}
	sort.Slice(compatible, func(left int, right int) bool { return compatible[left].ID < compatible[right].ID })
	if len(compatible) < 2 {
		t.Skipf("two-Host parallel smoke requires at least two compatible live Maya Hosts in Target Profile %q; found %d", options.TargetProfile, len(compatible))
	}
	return compatible[:2]
}

func awaitLiveParallelAgentExit(t *testing.T, agent *parallelTestAgent, timeout time.Duration) {
	t.Helper()
	select {
	case code := <-agent.done:
		if code != 0 {
			t.Fatalf("live Windows Host Agent %s exit code = %d; stdout: %s stderr: %s", agent.agentID, code, agent.stdout.String(), agent.stderr.String())
		}
	case <-time.After(timeout):
		t.Fatalf("live Windows Host Agent %s did not finish; stdout: %s stderr: %s", agent.agentID, agent.stdout.String(), agent.stderr.String())
	}
}

func awaitLiveParallelRunExit(t *testing.T, run *parallelTestRun, timeout time.Duration) {
	t.Helper()
	select {
	case code := <-run.done:
		if code != 0 {
			t.Fatalf("live parallel Scenario exit code = %d; stdout: %s stderr: %s", code, run.stdout.String(), run.stderr.String())
		}
	case <-time.After(timeout):
		t.Fatalf("live parallel Scenario did not finish; stdout: %s stderr: %s", run.stdout.String(), run.stderr.String())
	}
}

func assertLiveParallelRunIsolation(t *testing.T, serverURL string, runtime runRuntime, handler *controlPlaneHandler, dataDir string, targetProfile string, proof liveParallelRunProof, allProofs []liveParallelRunProof) {
	t.Helper()
	serverRepo := filepath.Join(dataDir, "runs", proof.runID, "repo")
	evidenceDir := filepath.Join(serverRepo, "artifacts", "maya-stall", proof.runID)
	bundle := assertLiveSmokeEvidenceBundle(t, evidenceDir)
	if bundle.RunID != proof.runID || bundle.Host != proof.host.ID || bundle.TargetProfile != targetProfile || bundle.Runtime.Profile != "ssh-sessiond" || !bundle.Runtime.LiveProofEligible {
		t.Fatalf("live parallel Evidence Bundle %s identity = %+v", proof.runID, bundle)
	}
	var result controlPlaneResultResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+proof.runID+"/result", runtime, &result); err != nil {
		t.Fatalf("read live parallel result %s: %v", proof.runID, err)
	}
	if !result.Final || !result.Success || result.State != "completed" || result.CleanupState != "completed" || !strings.Contains(result.Evidence, proof.runID) {
		t.Fatalf("live parallel result %s = %+v", proof.runID, result)
	}
	var record runLedgerRecord
	if err := readPrivateJSON(filepath.Join(serverRepo, ".maya-stall", "state", "ledger", "runs", proof.runID, "run.json"), &record); err != nil {
		t.Fatalf("read live parallel Run Ledger %s: %v", proof.runID, err)
	}
	if record.RunID != proof.runID || record.Host != proof.host.ID || record.State != "completed" || !strings.Contains(record.EvidenceDir, proof.runID) {
		t.Fatalf("live parallel Run Ledger %s = %+v", proof.runID, record)
	}
	for _, unrelated := range allProofs {
		if unrelated.runID == proof.runID {
			continue
		}
		if strings.Contains(string(mustJSON(t, bundle)), unrelated.runID) || strings.Contains(evidenceDir, unrelated.runID) || strings.Contains(runLedgerDir(serverRepo, proof.runID), unrelated.runID) {
			t.Fatalf("live Run %s evidence or ledger references unrelated Run %s", proof.runID, unrelated.runID)
		}
	}
	assertLiveFreshRunSessionStopped(t, proof.host, bundle.BrokerSession)
	assertLiveSharedRemoteWorkspaceRemoved(t, proof.host, proof.runID)
	if _, err := os.Lstat(filepath.Join(proof.agentRoot, "runs", proof.runID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live Run %s retained Agent workspace: %v", proof.runID, err)
	}
	if _, err := os.Lstat(handler.queuePath(proof.runID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live Run %s retained queue state: %v", proof.runID, err)
	}
	if _, err := os.Lstat(handler.hostLockPath(proof.host.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live Run %s retained shared Host Lock: %v", proof.runID, err)
	}
	if layer := remoteHostLockLayer(proof.host); layer.Status != "ok" || layer.State != "unlocked" {
		t.Fatalf("live Run %s retained Maya Host Lock: %+v", proof.runID, layer)
	}
}
