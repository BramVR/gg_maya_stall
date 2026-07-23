package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParallelHostAgentsExecuteTwoRunsAndQueueThird(t *testing.T) {
	repoDir := writeRunConfigFixture(t)
	dataDir := privateTempDir(t)
	handlerValue, err := newControlPlaneHandler(dataDir, "operator-token", defaultRunRuntime())
	if err != nil {
		t.Fatalf("create Control Plane handler: %v", err)
	}
	handler := handlerValue.(*controlPlaneHandler)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	t.Setenv(defaultControlPlaneTokenEnv, "operator-token")
	t.Setenv("TEST_MAYA_STALL_HOST_AGENT_CREDENTIAL", testHostAgentCredential)
	runtime := defaultRunRuntime()
	runtime.ControlPlaneHTTPClient = server.Client()

	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, "windows-agent-01", "maya-win-01")
	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, "windows-agent-02", "maya-win-02")

	firstAgent := startBlockedParallelTestAgent(t, repoDir, server.URL, runtime, "windows-agent-01", "maya-win-01", privateTempDir(t), nil)
	secondAgent := startBlockedParallelTestAgent(t, repoDir, server.URL, runtime, "windows-agent-02", "maya-win-02", privateTempDir(t), nil)
	waitForHostAgentState(t, server.Client(), server.URL, firstAgent.agentID, "ready")
	waitForHostAgentState(t, server.Client(), server.URL, secondAgent.agentID, "ready")

	firstRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	firstAccepted := awaitParallelAccepted(t, firstRun)
	firstSession := awaitParallelSession(t, firstAgent)
	if firstSession.SessionID != "fake-"+firstAccepted.RunID {
		t.Fatalf("first broker session = %+v, want Run %s", firstSession, firstAccepted.RunID)
	}

	secondRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	secondAccepted := awaitParallelAccepted(t, secondRun)
	secondSession := awaitParallelSession(t, secondAgent)
	if secondSession.SessionID != "fake-"+secondAccepted.RunID {
		t.Fatalf("second broker session = %+v, want Run %s", secondSession, secondAccepted.RunID)
	}
	assertParallelAgentOwnsRun(t, server, handler, firstAgent, firstAccepted.RunID)
	assertParallelAgentOwnsRun(t, server, handler, secondAgent, secondAccepted.RunID)
	assertActiveParallelWorkspace(t, firstAgent, firstAccepted.RunID, secondAccepted.RunID)
	assertActiveParallelWorkspace(t, secondAgent, secondAccepted.RunID, firstAccepted.RunID)

	thirdRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	thirdAccepted := awaitParallelAccepted(t, thirdRun)
	var queued controlPlaneStatusResponse
	if err := getControlPlaneJSON(server.URL, "", "/v1/runs/"+thirdAccepted.RunID+"/status", runtime, &queued); err != nil {
		t.Fatalf("read third queued Run status: %v", err)
	}
	if queued.State != "queued" || queued.QueuePosition != 1 || queued.WaitReason != "compatible-hosts-busy" || queued.Host != "" {
		t.Fatalf("third Run queue status = %+v", queued)
	}
	assertParallelAgentOwnsRun(t, server, handler, firstAgent, firstAccepted.RunID)
	assertParallelAgentOwnsRun(t, server, handler, secondAgent, secondAccepted.RunID)
	if _, err := os.Lstat(filepath.Join(dataDir, "assignments", thirdAccepted.RunID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("queued third Run received an assignment: %v", err)
	}

	firstAgent.release()
	awaitParallelExit(t, "first Windows Host Agent", firstAgent.done, 0)
	awaitParallelExit(t, "first Scenario", firstRun.done, 0)

	replacementAgent := startBlockedParallelTestAgent(t, repoDir, server.URL, runtime, firstAgent.agentID, firstAgent.hostID, firstAgent.workRoot, nil)
	thirdSession := awaitParallelSession(t, replacementAgent)
	if thirdSession.SessionID != "fake-"+thirdAccepted.RunID {
		t.Fatalf("third broker session = %+v, want queued Run %s", thirdSession, thirdAccepted.RunID)
	}
	assertParallelAgentOwnsRun(t, server, handler, replacementAgent, thirdAccepted.RunID)
	assertParallelAgentOwnsRun(t, server, handler, secondAgent, secondAccepted.RunID)

	secondAgent.release()
	replacementAgent.release()
	awaitParallelExit(t, "second Windows Host Agent", secondAgent.done, 0)
	awaitParallelExit(t, "replacement Windows Host Agent", replacementAgent.done, 0)
	awaitParallelExit(t, "second Scenario", secondRun.done, 0)
	awaitParallelExit(t, "third Scenario", thirdRun.done, 0)

	for _, cleaned := range []struct {
		runID string
		root  string
	}{
		{runID: firstAccepted.RunID, root: firstAgent.workRoot},
		{runID: secondAccepted.RunID, root: secondAgent.workRoot},
		{runID: thirdAccepted.RunID, root: replacementAgent.workRoot},
	} {
		if _, err := os.Lstat(filepath.Join(cleaned.root, "runs", cleaned.runID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("completed Run %s retained Agent workspace: %v", cleaned.runID, err)
		}
	}
	runIDs := []string{firstAccepted.RunID, secondAccepted.RunID, thirdAccepted.RunID}
	for _, runID := range runIDs {
		assertCompletedParallelRunIsolation(t, server.URL, runtime, handler, dataDir, runID, runIDs)
	}
}

func TestParallelHostAgentFailureIsIsolatedByRunID(t *testing.T) {
	repoDir := writeRunConfigFixture(t)
	dataDir := privateTempDir(t)
	handlerValue, err := newControlPlaneHandler(dataDir, "operator-token", defaultRunRuntime())
	if err != nil {
		t.Fatalf("create Control Plane handler: %v", err)
	}
	handler := handlerValue.(*controlPlaneHandler)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	t.Setenv(defaultControlPlaneTokenEnv, "operator-token")
	t.Setenv("TEST_MAYA_STALL_HOST_AGENT_CREDENTIAL", testHostAgentCredential)
	runtime := defaultRunRuntime()
	runtime.ControlPlaneHTTPClient = server.Client()

	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, "windows-agent-01", "maya-win-01")
	enrollParallelTestHostAgent(t, repoDir, server.URL, runtime, "windows-agent-02", "maya-win-02")
	injectedFailure := errors.New("injected failure after one Maya UI Session started")
	failingAgent := startBlockedParallelTestAgent(t, repoDir, server.URL, runtime, "windows-agent-01", "maya-win-01", privateTempDir(t), injectedFailure)
	passingAgent := startBlockedParallelTestAgent(t, repoDir, server.URL, runtime, "windows-agent-02", "maya-win-02", privateTempDir(t), nil)
	waitForHostAgentState(t, server.Client(), server.URL, failingAgent.agentID, "ready")
	waitForHostAgentState(t, server.Client(), server.URL, passingAgent.agentID, "ready")

	failingRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	failingAccepted := awaitParallelAccepted(t, failingRun)
	_ = awaitParallelSession(t, failingAgent)
	passingRun := startParallelTestRun(t, repoDir, server.URL, runtime)
	passingAccepted := awaitParallelAccepted(t, passingRun)
	_ = awaitParallelSession(t, passingAgent)
	assertParallelAgentOwnsRun(t, server, handler, failingAgent, failingAccepted.RunID)
	assertParallelAgentOwnsRun(t, server, handler, passingAgent, passingAccepted.RunID)

	failingAgent.release()
	awaitParallelExit(t, "failing Windows Host Agent", failingAgent.done, 1)
	awaitParallelExit(t, "failing Scenario", failingRun.done, 1)

	if _, err := os.Lstat(filepath.Join(failingAgent.workRoot, "runs", failingAccepted.RunID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Run retained Agent workspace: %v", err)
	}
	assertParallelAgentOwnsRun(t, server, handler, passingAgent, passingAccepted.RunID)
	assertActiveParallelWorkspace(t, passingAgent, passingAccepted.RunID, failingAccepted.RunID)
	if _, err := os.Lstat(handler.hostLockPath(failingAgent.hostID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Run retained Host Lock: %v", err)
	}
	assertFailedParallelRunIsolation(t, server.URL, runtime, dataDir, failingAccepted.RunID, passingAccepted.RunID, injectedFailure.Error())

	passingAgent.release()
	awaitParallelExit(t, "passing Windows Host Agent", passingAgent.done, 0)
	awaitParallelExit(t, "passing Scenario", passingRun.done, 0)
	if _, err := os.Lstat(filepath.Join(passingAgent.workRoot, "runs", passingAccepted.RunID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("passing Run retained Agent workspace: %v", err)
	}
	assertCompletedParallelRunIsolation(t, server.URL, runtime, handler, dataDir, passingAccepted.RunID, []string{failingAccepted.RunID, passingAccepted.RunID})
}

type parallelTestAgent struct {
	agentID     string
	hostID      string
	workRoot    string
	started     chan brokerSessionIdentity
	releaseOnce sync.Once
	releaseRun  chan struct{}
	done        chan int
	stdout      bytes.Buffer
	stderr      bytes.Buffer
}

type parallelTestRun struct {
	accepted chan runOutcome
	done     chan int
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

func enrollParallelTestHostAgent(t *testing.T, repoDir string, serverURL string, runtime runRuntime, agentID string, hostID string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := RunWithRuntime([]string{
		"control-plane", "enroll-agent", "--control-plane", serverURL,
		"--agent-id", agentID, "--host", hostID,
		"--credential-env", "TEST_MAYA_STALL_HOST_AGENT_CREDENTIAL",
	}, &stdout, &stderr, repoDir, "test-version", runtime); code != 0 {
		t.Fatalf("enroll Windows Host Agent %s exit code = %d; stdout: %s stderr: %s", agentID, code, stdout.String(), stderr.String())
	}
}

func startBlockedParallelTestAgent(t *testing.T, repoDir string, serverURL string, runtime runRuntime, agentID string, hostID string, workRoot string, releaseErr error) *parallelTestAgent {
	t.Helper()
	return startBlockedParallelHostAgent(t, repoDir, serverURL, runtime, agentID, hostID, workRoot, "", releaseErr)
}

func startBlockedParallelHostAgent(t *testing.T, repoDir string, serverURL string, runtime runRuntime, agentID string, hostID string, workRoot string, hostConfig string, releaseErr error) *parallelTestAgent {
	t.Helper()
	agent := &parallelTestAgent{
		agentID: agentID, hostID: hostID, workRoot: workRoot,
		started: make(chan brokerSessionIdentity, 1), releaseRun: make(chan struct{}), done: make(chan int, 1),
	}
	t.Cleanup(agent.release)
	agentRuntime := runtime
	agentRuntime.SessionStarted = func(session brokerSessionIdentity) error {
		agent.started <- session
		<-agent.releaseRun
		return releaseErr
	}
	args := []string{
		"host-agent", "run-once", "--control-plane", serverURL,
		"--agent-id", agentID, "--host", hostID, "--work-root", workRoot,
		"--credential-env", "TEST_MAYA_STALL_HOST_AGENT_CREDENTIAL",
	}
	if hostConfig != "" {
		args = append(args, "--host-config", hostConfig)
	}
	go func() {
		agent.done <- RunWithRuntime(args, &agent.stdout, &agent.stderr, repoDir, "test-version", agentRuntime)
	}()
	return agent
}

func (agent *parallelTestAgent) release() {
	agent.releaseOnce.Do(func() { close(agent.releaseRun) })
}

func startParallelTestRun(t *testing.T, repoDir string, serverURL string, runtime runRuntime) *parallelTestRun {
	t.Helper()
	run := &parallelTestRun{accepted: make(chan runOutcome, 1), done: make(chan int, 1)}
	runRuntime := runtime
	runRuntime.Accepted = func(outcome runOutcome) { run.accepted <- outcome }
	go func() {
		run.done <- RunWithRuntime([]string{"run", "--json", "--control-plane", serverURL, "smoke"}, &run.stdout, &run.stderr, repoDir, "test-version", runRuntime)
	}()
	return run
}

func awaitParallelAccepted(t *testing.T, run *parallelTestRun) runOutcome {
	t.Helper()
	return awaitParallelAcceptedWithin(t, run, 2*time.Second)
}

func awaitParallelAcceptedWithin(t *testing.T, run *parallelTestRun, timeout time.Duration) runOutcome {
	t.Helper()
	select {
	case accepted := <-run.accepted:
		return accepted
	case <-time.After(timeout):
		t.Fatalf("Control Plane did not accept parallel Scenario; stdout: %s stderr: %s", run.stdout.String(), run.stderr.String())
		return runOutcome{}
	}
}

func awaitParallelSession(t *testing.T, agent *parallelTestAgent) brokerSessionIdentity {
	t.Helper()
	return awaitParallelSessionWithin(t, agent, 2*time.Second)
}

func awaitParallelSessionWithin(t *testing.T, agent *parallelTestAgent, timeout time.Duration) brokerSessionIdentity {
	t.Helper()
	select {
	case session := <-agent.started:
		return session
	case code := <-agent.done:
		t.Fatalf("Windows Host Agent %s exited before starting a session with code %d; stdout: %s stderr: %s", agent.agentID, code, agent.stdout.String(), agent.stderr.String())
		return brokerSessionIdentity{}
	case <-time.After(timeout):
		t.Fatalf("Windows Host Agent %s did not start its assigned session", agent.agentID)
		return brokerSessionIdentity{}
	}
}

func awaitParallelExit(t *testing.T, name string, done <-chan int, want int) {
	t.Helper()
	awaitParallelExitWithin(t, name, done, want, 2*time.Second)
}

func awaitParallelExitWithin(t *testing.T, name string, done <-chan int, want int, timeout time.Duration) {
	t.Helper()
	select {
	case code := <-done:
		if code != want {
			t.Fatalf("%s exit code = %d, want %d", name, code, want)
		}
	case <-time.After(timeout):
		t.Fatalf("%s did not finish", name)
	}
}

func assertParallelAgentOwnsRun(t *testing.T, server *httptest.Server, handler *controlPlaneHandler, agent *parallelTestAgent, runID string) {
	t.Helper()
	assertParallelAgentOwnsRunWithToken(t, server, handler, agent, runID, "operator-token")
	var lock hostAgentLockRecord
	if err := readPrivateJSON(handler.hostLockPath(agent.hostID), &lock); err != nil {
		t.Fatalf("read fake Host Lock for %s: %v", agent.hostID, err)
	}
	if lock.BrokerSession.SessionID != "fake-"+runID {
		t.Fatalf("fake Host Lock for %s bound session %+v, want Run %s", agent.hostID, lock.BrokerSession, runID)
	}
}

func assertParallelAgentOwnsRunWithToken(t *testing.T, server *httptest.Server, handler *controlPlaneHandler, agent *parallelTestAgent, runID string, operatorToken string) {
	t.Helper()
	status := readHostAgentStatusWithToken(t, server.Client(), server.URL, operatorToken, agent.agentID)
	if status.State != "running" || status.RunID != runID || status.HostID != agent.hostID || status.Slots != 1 {
		t.Fatalf("Windows Host Agent %s active ownership = %+v, want Run %s", agent.agentID, status, runID)
	}
	var lock hostAgentLockRecord
	if err := readPrivateJSON(handler.hostLockPath(agent.hostID), &lock); err != nil {
		t.Fatalf("read Host Lock for %s: %v", agent.hostID, err)
	}
	if lock.RunID != runID || lock.AgentID != agent.agentID || lock.HostID != agent.hostID || lock.LockToken == "" || lock.BrokerSession == nil || lock.BrokerSession.BrokerAdapter == "" || lock.BrokerSession.SessionID == "" {
		t.Fatalf("Host Lock for %s = %+v, want isolated Run %s", agent.hostID, lock, runID)
	}
}

func assertActiveParallelWorkspace(t *testing.T, agent *parallelTestAgent, runID string, unrelatedRunID string) {
	t.Helper()
	repoDir := filepath.Join(agent.workRoot, "runs", runID, "repo")
	for _, path := range []string{
		filepath.Join(repoDir, "maya", "smoke.py"),
		filepath.Join(repoDir, ".maya-stall", "state", "runs", runID, "payload", "mayaScripts", "maya", "smoke.py"),
		filepath.Join(repoDir, ".maya-stall", "state", "runs", runID, "events.jsonl"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("active Run %s missing isolated path %s: %v", runID, path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(agent.workRoot, "runs", unrelatedRunID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Host %s workspace contains unrelated Run %s: %v", agent.hostID, unrelatedRunID, err)
	}
}

func assertCompletedParallelRunIsolation(t *testing.T, serverURL string, runtime runRuntime, handler *controlPlaneHandler, dataDir string, runID string, allRunIDs []string) {
	t.Helper()
	serverRepo := filepath.Join(dataDir, "runs", runID, "repo")
	record, err := readRunLedgerRecord(serverRepo, runID)
	if err != nil {
		t.Fatalf("read Run Ledger %s: %v", runID, err)
	}
	wantEvidence := filepath.ToSlash(filepath.Join("artifacts", "maya-stall", runID))
	if record.RunID != runID || record.State != "completed" || record.EvidenceDir != wantEvidence {
		t.Fatalf("Run Ledger %s = %+v", runID, record)
	}
	var result controlPlaneResultResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/result", runtime, &result); err != nil {
		t.Fatalf("read result %s: %v", runID, err)
	}
	if !result.Final || !result.Success || result.State != "completed" || result.CleanupState != "completed" || !strings.Contains(result.Evidence, runID) {
		t.Fatalf("result %s = %+v", runID, result)
	}
	var events controlPlaneEventsResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/events", runtime, &events); err != nil {
		t.Fatalf("read events %s: %v", runID, err)
	}
	eventsJSON := string(mustJSON(t, events.Events))
	if !strings.Contains(eventsJSON, "fake-"+runID) {
		t.Fatalf("events for %s do not contain its broker session: %s", runID, eventsJSON)
	}
	var logs controlPlaneLogsResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/logs", runtime, &logs); err != nil {
		t.Fatalf("read logs %s: %v", runID, err)
	}
	if logs.RunID != runID || !strings.Contains(logs.Content, "fake Session Broker ran Scenario") {
		t.Fatalf("logs %s = %+v", runID, logs)
	}
	var bundle evidenceBundle
	evidencePath := filepath.Join(serverRepo, "artifacts", "maya-stall", runID, evidenceBundleFileName)
	readJSONFile(t, evidencePath, &bundle)
	if bundle.RunID != runID || bundle.Status != resultStatusPassed || bundle.BrokerSession == nil || bundle.BrokerSession.SessionID != "fake-"+runID {
		t.Fatalf("Evidence Bundle %s = %+v", runID, bundle)
	}
	var assignment hostAgentAssignmentRecord
	if err := readPrivateJSON(filepath.Join(dataDir, "assignments", runID+".json"), &assignment); err != nil {
		t.Fatalf("read completed assignment %s: %v", runID, err)
	}
	if assignment.RunID != runID || assignment.HostID != bundle.Host || assignment.State != "completed" {
		t.Fatalf("completed assignment %s = %+v", runID, assignment)
	}
	for _, unrelatedRunID := range allRunIDs {
		if unrelatedRunID == runID {
			continue
		}
		for surface, content := range map[string]string{"events": eventsJSON, "logs": logs.Content, "evidence path": evidencePath, "ledger path": runLedgerDir(serverRepo, runID)} {
			if strings.Contains(content, unrelatedRunID) {
				t.Fatalf("%s for Run %s references unrelated Run %s: %s", surface, runID, unrelatedRunID, content)
			}
		}
	}
	if _, err := os.Lstat(handler.queuePath(runID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed Run %s retained queue state: %v", runID, err)
	}
	if _, err := os.Lstat(handler.hostLockPath(bundle.Host)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed Run %s retained Host Lock: %v", runID, err)
	}
}

func assertFailedParallelRunIsolation(t *testing.T, serverURL string, runtime runRuntime, dataDir string, runID string, unrelatedRunID string, diagnostic string) {
	t.Helper()
	serverRepo := filepath.Join(dataDir, "runs", runID, "repo")
	record, err := readRunLedgerRecord(serverRepo, runID)
	if err != nil {
		t.Fatalf("read failed Run Ledger %s: %v", runID, err)
	}
	if record.RunID != runID || record.State != "failed" || !strings.Contains(record.EvidenceDir, runID) {
		t.Fatalf("failed Run Ledger %s = %+v", runID, record)
	}
	var result controlPlaneResultResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/result", runtime, &result); err != nil {
		t.Fatalf("read failed result %s: %v", runID, err)
	}
	if !result.Final || result.Success || result.State != "failed" || result.CleanupState != "completed" || !strings.Contains(result.Evidence, runID) {
		t.Fatalf("failed result %s = %+v", runID, result)
	}
	var events controlPlaneEventsResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/events", runtime, &events); err != nil {
		t.Fatalf("read failed events %s: %v", runID, err)
	}
	eventsJSON := string(mustJSON(t, events.Events))
	if !strings.Contains(eventsJSON, "fake-"+runID) || !strings.Contains(eventsJSON, diagnostic) || strings.Contains(eventsJSON, unrelatedRunID) {
		t.Fatalf("failed events for %s are not isolated: %s", runID, eventsJSON)
	}
	var logs controlPlaneLogsResponse
	if err := getControlPlaneJSON(serverURL, "", "/v1/runs/"+runID+"/logs", runtime, &logs); err != nil {
		t.Fatalf("read failed logs %s: %v", runID, err)
	}
	if logs.RunID != runID || !strings.Contains(logs.Content, diagnostic) || strings.Contains(logs.Content, unrelatedRunID) {
		t.Fatalf("failed logs %s = %+v", runID, logs)
	}
	var bundle evidenceBundle
	evidencePath := filepath.Join(serverRepo, "artifacts", "maya-stall", runID, evidenceBundleFileName)
	readJSONFile(t, evidencePath, &bundle)
	if bundle.RunID != runID || bundle.Status != resultStatusFailed || bundle.Failure == nil || bundle.Failure.CleanupState != "completed" || bundle.BrokerSession == nil || bundle.BrokerSession.SessionID != "fake-"+runID {
		t.Fatalf("failed Evidence Bundle %s = %+v", runID, bundle)
	}
	if strings.Contains(string(mustJSON(t, bundle)), unrelatedRunID) {
		t.Fatalf("failed Evidence Bundle %s references unrelated Run %s", runID, unrelatedRunID)
	}
	var assignment hostAgentAssignmentRecord
	if err := readPrivateJSON(filepath.Join(dataDir, "assignments", runID+".json"), &assignment); err != nil {
		t.Fatalf("read failed completed assignment %s: %v", runID, err)
	}
	if assignment.RunID != runID || assignment.State != "completed" || assignment.BrokerSession == nil || assignment.BrokerSession.SessionID != "fake-"+runID {
		t.Fatalf("failed completed assignment %s = %+v", runID, assignment)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	output, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode test value: %v", err)
	}
	return output
}
