package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const hostAgentAPIVersion = 1
const maximumHostAgentCredentialBytes = 4096
const minimumHostAgentCredentialBytes = 32
const hostAgentSessionLease = time.Minute
const hostAgentHeartbeatInterval = 15 * time.Second
const hostAgentHeartbeatRequestTimeout = 10 * time.Second

type controlPlaneEnrollAgentOptions struct {
	ControlPlane  string
	AgentID       string
	HostID        string
	CredentialEnv string
	TokenEnv      string
}

type hostAgentRunOnceOptions struct {
	ControlPlane  string
	AgentID       string
	HostID        string
	WorkRoot      string
	CredentialEnv string
	SessionID     string
}

type hostAgentEnrollmentRequest struct {
	Version    int    `json:"version"`
	AgentID    string `json:"agentId"`
	HostID     string `json:"hostId"`
	Credential string `json:"credential"`
}

type hostAgentEnrollmentRecord struct {
	Version          int    `json:"version"`
	AgentID          string `json:"agentId"`
	HostID           string `json:"hostId"`
	CredentialSHA256 string `json:"credentialSha256"`
	EnrolledAt       string `json:"enrolledAt"`
}

type hostAgentRegistrationRequest struct {
	Version int    `json:"version"`
	AgentID string `json:"agentId"`
	HostID  string `json:"hostId"`
	Slots   int    `json:"slots"`
}

type hostAgentNextRequest struct {
	Version   int    `json:"version"`
	SessionID string `json:"sessionId"`
}

type hostAgentStatusResponse struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	AgentID   string `json:"agentId"`
	HostID    string `json:"hostId"`
	Slots     int    `json:"slots"`
	State     string `json:"state"`
	RunID     string `json:"runId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

type hostAgentAssignmentResponse struct {
	Version    int                    `json:"version"`
	Kind       string                 `json:"kind"`
	RunID      string                 `json:"runId"`
	AgentID    string                 `json:"agentId"`
	HostID     string                 `json:"hostId"`
	LockToken  string                 `json:"lockToken"`
	Submission controlPlaneSubmission `json:"submission"`
}

type hostAgentLockRequest struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	LockToken string `json:"lockToken"`
	SessionID string `json:"sessionId"`
}

type hostAgentCompletionRequest struct {
	Version   int                `json:"version"`
	RunID     string             `json:"runId"`
	LockToken string             `json:"lockToken"`
	SessionID string             `json:"sessionId"`
	Terminal  runCommandJSON     `json:"terminal"`
	Files     []controlPlaneFile `json:"files"`
}

type hostAgentFailureRequest struct {
	Version    int    `json:"version"`
	RunID      string `json:"runId"`
	LockToken  string `json:"lockToken"`
	SessionID  string `json:"sessionId"`
	Diagnostic string `json:"diagnostic"`
	Quarantine bool   `json:"quarantine,omitempty"`
}

type hostAgentLockRecord struct {
	Version   int    `json:"version"`
	RunID     string `json:"runId"`
	AgentID   string `json:"agentId"`
	HostID    string `json:"hostId"`
	LockToken string `json:"lockToken"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
}

type hostAgentAssignmentRecord struct {
	Version        int                    `json:"version"`
	RunID          string                 `json:"runId"`
	AgentID        string                 `json:"agentId"`
	HostID         string                 `json:"hostId"`
	LockToken      string                 `json:"lockToken"`
	State          string                 `json:"state"`
	CreatedAt      string                 `json:"createdAt"`
	Submission     controlPlaneSubmission `json:"submission"`
	Terminal       *runCommandJSON        `json:"terminal,omitempty"`
	TerminalLedger *runLedgerRecord       `json:"terminalLedger,omitempty"`
	AssignedLedger *runLedgerRecord       `json:"assignedLedger,omitempty"`
}

type controlPlaneHostAgent struct {
	enrollment        hostAgentEnrollmentRecord
	status            hostAgentStatusResponse
	notify            chan struct{}
	sessionExpiresAt  time.Time
	takeoverNotBefore time.Time
}

type controlPlaneHostAgentAssignment struct {
	record                hostAgentAssignmentRecord
	repoDir               string
	done                  chan struct{}
	outcome               runOutcome
	err                   error
	run                   *freshRunLifecycle
	finishing             bool
	finished              bool
	originalLedger        *runLedgerRecord
	terminalLedger        *runLedgerRecord
	sharedFakeHostRelease func() error
}

type controlPlaneHTTPStatusError struct {
	StatusCode int
}

func (err *controlPlaneHTTPStatusError) Error() string {
	return fmt.Sprintf("Control Plane request failed with HTTP %d", err.StatusCode)
}

func validateHostAgentID(agentID string) error {
	return validateHostAgentStateID(agentID, "Windows Host Agent")
}

func validateHostAgentHostID(hostID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	return validateHostAgentStateID(hostID, "Maya Host")
}

func validateHostAgentStateID(value string, label string) error {
	if len(value) == 0 || len(value) > 63 {
		return fmt.Errorf("%s id must contain 1 through 63 portable characters", label)
	}
	for index, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' && index > 0 && index < len(value)-1 {
			continue
		}
		return fmt.Errorf("%s id must use lowercase ASCII letters, digits, and interior hyphens", label)
	}
	switch value {
	case "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9", "lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return fmt.Errorf("%s id %q is reserved on Windows", label, value)
	}
	return nil
}

func parseControlPlaneEnrollAgentArgs(args []string) (controlPlaneEnrollAgentOptions, error) {
	var options controlPlaneEnrollAgentOptions
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--control-plane", "--agent-id", "--host", "--credential-env", "--token-env":
			index++
			if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "--") {
				return controlPlaneEnrollAgentOptions{}, newUsageError("%s needs a value", flag)
			}
			switch flag {
			case "--control-plane":
				options.ControlPlane = args[index]
			case "--agent-id":
				options.AgentID = args[index]
			case "--host":
				options.HostID = args[index]
			case "--credential-env":
				options.CredentialEnv = args[index]
			case "--token-env":
				options.TokenEnv = args[index]
			}
		default:
			return controlPlaneEnrollAgentOptions{}, newUsageError("unknown control-plane enroll-agent option %q", flag)
		}
	}
	if _, err := parseControlPlaneURL(options.ControlPlane); err != nil {
		return controlPlaneEnrollAgentOptions{}, err
	}
	if err := validateHostAgentID(options.AgentID); err != nil {
		return controlPlaneEnrollAgentOptions{}, newUsageError("invalid Windows Host Agent id: %v", err)
	}
	if err := validateHostAgentHostID(options.HostID); err != nil {
		return controlPlaneEnrollAgentOptions{}, err
	}
	if options.CredentialEnv == "" {
		return controlPlaneEnrollAgentOptions{}, newUsageError("control-plane enroll-agent needs --credential-env")
	}
	return options, nil
}

func parseHostAgentRunOnceArgs(args []string, workDir string) (hostAgentRunOnceOptions, error) {
	var options hostAgentRunOnceOptions
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--control-plane", "--agent-id", "--host", "--work-root", "--credential-env":
			index++
			if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "--") {
				return hostAgentRunOnceOptions{}, newUsageError("%s needs a value", flag)
			}
			switch flag {
			case "--control-plane":
				options.ControlPlane = args[index]
			case "--agent-id":
				options.AgentID = args[index]
			case "--host":
				options.HostID = args[index]
			case "--work-root":
				options.WorkRoot = resolveFromRepo(workDir, args[index])
			case "--credential-env":
				options.CredentialEnv = args[index]
			}
		default:
			return hostAgentRunOnceOptions{}, newUsageError("unknown host-agent run-once option %q", flag)
		}
	}
	if _, err := parseControlPlaneURL(options.ControlPlane); err != nil {
		return hostAgentRunOnceOptions{}, err
	}
	if err := validateHostAgentID(options.AgentID); err != nil {
		return hostAgentRunOnceOptions{}, newUsageError("invalid Windows Host Agent id: %v", err)
	}
	if err := validateHostAgentHostID(options.HostID); err != nil {
		return hostAgentRunOnceOptions{}, err
	}
	if options.WorkRoot == "" {
		return hostAgentRunOnceOptions{}, newUsageError("host-agent run-once needs --work-root")
	}
	if options.CredentialEnv == "" {
		return hostAgentRunOnceOptions{}, newUsageError("host-agent run-once needs --credential-env")
	}
	return options, nil
}

func enrollControlPlaneHostAgent(options controlPlaneEnrollAgentOptions, runtime runRuntime, stdout io.Writer) error {
	credential, ok := os.LookupEnv(options.CredentialEnv)
	if !ok || len(credential) < minimumHostAgentCredentialBytes {
		return fmt.Errorf("Windows Host Agent credential environment variable %s must contain at least %d bytes", options.CredentialEnv, minimumHostAgentCredentialBytes)
	}
	if len(credential) > maximumHostAgentCredentialBytes {
		return fmt.Errorf("Windows Host Agent credential exceeds size limit")
	}
	request := hostAgentEnrollmentRequest{
		Version: hostAgentAPIVersion, AgentID: options.AgentID, HostID: options.HostID, Credential: credential,
	}
	var status hostAgentStatusResponse
	if err := postControlPlaneJSON(options.ControlPlane, options.TokenEnv, "/v1/host-agents/enroll", request, runtime, http.StatusCreated, &status); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "agent: %s\nhost: %s\nstate: %s\nslots: %d\n", status.AgentID, status.HostID, status.State, status.Slots)
	return err
}

func postControlPlaneJSON(rawURL string, tokenEnv string, path string, body any, runtime runRuntime, wantStatus int, target any) error {
	return postControlPlaneJSONWithLimit(rawURL, tokenEnv, path, body, runtime, wantStatus, target, maximumControlPlaneDefaultResponseBytes)
}

func postControlPlaneJSONWithLimit(rawURL string, tokenEnv string, path string, body any, runtime runRuntime, wantStatus int, target any, responseLimit int64) error {
	return postControlPlaneJSONWithLimitContext(context.Background(), rawURL, tokenEnv, path, body, runtime, wantStatus, target, responseLimit)
}

func postControlPlaneJSONWithLimitContext(ctx context.Context, rawURL string, tokenEnv string, path string, body any, runtime runRuntime, wantStatus int, target any, responseLimit int64) error {
	endpoint, err := parseControlPlaneURL(rawURL)
	if err != nil {
		return err
	}
	if tokenEnv == "" {
		tokenEnv = defaultControlPlaneTokenEnv
	}
	token, ok := os.LookupEnv(tokenEnv)
	if !ok || token == "" {
		return fmt.Errorf("Control Plane credential environment variable %s is not set", tokenEnv)
	}
	content, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if len(content) > maximumControlPlaneSubmissionBytes {
		return fmt.Errorf("Control Plane request exceeds size limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint.String(), "/")+path, bytes.NewReader(content))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := controlPlaneHTTPClient(runtime.ControlPlaneHTTPClient).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != wantStatus {
		return &controlPlaneHTTPStatusError{StatusCode: response.StatusCode}
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	return decodeBoundedControlPlaneJSON(response.Body, responseLimit, target)
}

func ensureHostAgentDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Windows Host Agent directory %s must be a directory, not a symlink", path)
	}
	return validateHostAgentDirectoryPermissions(path, info)
}

func (handler *controlPlaneHandler) loadHostAgentState() error {
	root := filepath.Join(handler.dataDir, "host-agents")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || validateHostAgentID(entry.Name()) != nil {
			return fmt.Errorf("invalid Windows Host Agent state directory %s", entry.Name())
		}
		content, err := os.ReadFile(filepath.Join(root, entry.Name(), "enrollment.json"))
		if err != nil {
			return err
		}
		var enrollment hostAgentEnrollmentRecord
		if err := json.Unmarshal(content, &enrollment); err != nil {
			return err
		}
		if enrollment.Version != hostAgentAPIVersion || enrollment.AgentID != entry.Name() || validateHostAgentHostID(enrollment.HostID) != nil || len(enrollment.CredentialSHA256) != sha256.Size*2 {
			return fmt.Errorf("invalid Windows Host Agent enrollment %s", entry.Name())
		}
		status := hostAgentStatusResponse{
			Version: hostAgentAPIVersion, Kind: "host-agent-status", AgentID: enrollment.AgentID,
			HostID: enrollment.HostID, Slots: 1, State: "offline",
		}
		var persistedStatus hostAgentStatusResponse
		if err := readPrivateJSON(filepath.Join(root, entry.Name(), "status.json"), &persistedStatus); err != nil {
			return err
		}
		if persistedStatus.Version != hostAgentAPIVersion || persistedStatus.Kind != "host-agent-status" || persistedStatus.AgentID != enrollment.AgentID || persistedStatus.HostID != enrollment.HostID || persistedStatus.Slots != 1 {
			return fmt.Errorf("invalid Windows Host Agent status %s", entry.Name())
		}
		if persistedStatus.State == "quarantined" {
			if validateRunID(persistedStatus.RunID) != nil || persistedStatus.SessionID != "" {
				return fmt.Errorf("invalid quarantined Windows Host Agent status %s", entry.Name())
			}
			status.State = "quarantined"
			status.RunID = persistedStatus.RunID
		}
		handler.hostAgents[entry.Name()] = &controlPlaneHostAgent{
			enrollment: enrollment,
			status:     status,
			notify:     make(chan struct{}),
		}
	}
	if err := handler.recoverHostAgentQuarantineMarkers(); err != nil {
		return err
	}
	if err := handler.recoverHostAgentTransactions(); err != nil {
		return err
	}
	lockEntries, err := os.ReadDir(filepath.Join(handler.dataDir, "host-locks"))
	if err != nil {
		return err
	}
	for _, entry := range lockEntries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return fmt.Errorf("invalid durable Host Lock path %s", entry.Name())
		}
		hostID := strings.TrimSuffix(entry.Name(), ".json")
		if validateHostAgentHostID(hostID) != nil {
			return fmt.Errorf("invalid durable Host Lock path %s", entry.Name())
		}
		var lock hostAgentLockRecord
		if err := readPrivateJSON(filepath.Join(handler.dataDir, "host-locks", entry.Name()), &lock); err != nil {
			return err
		}
		if lock.Version != hostAgentAPIVersion || lock.HostID != hostID || validateRunID(lock.RunID) != nil || lock.LockToken == "" || lock.State != "assigned" && lock.State != "confirmed" && lock.State != "finishing" && lock.State != "quarantined" {
			return fmt.Errorf("invalid durable Host Lock for %s", hostID)
		}
		agent := handler.hostAgents[lock.AgentID]
		if agent == nil || agent.enrollment.HostID != hostID || agent.status.RunID != "" && (agent.status.State != "quarantined" || agent.status.RunID != lock.RunID) {
			return fmt.Errorf("durable Host Lock for %s has no unique enrollment", hostID)
		}
		var assignment hostAgentAssignmentRecord
		if err := readPrivateJSON(filepath.Join(handler.dataDir, "assignments", lock.RunID+".json"), &assignment); err != nil {
			return err
		}
		if assignment.Version != hostAgentAPIVersion || assignment.RunID != lock.RunID || assignment.AgentID != lock.AgentID || assignment.HostID != lock.HostID || !sameLockToken(assignment.LockToken, lock.LockToken) || assignment.State != lock.State {
			return fmt.Errorf("durable assignment for %s does not match its Host Lock", lock.RunID)
		}
		if assignment.State == "finishing" {
			if assignment.Terminal == nil || assignment.TerminalLedger == nil {
				return fmt.Errorf("finishing Host Agent assignment %s has no terminal state", assignment.RunID)
			}
			if err := validateFinishingHostAgentAssignment(assignment); err != nil {
				return fmt.Errorf("invalid finishing Host Agent assignment %s: %w", assignment.RunID, err)
			}
			repoDir := filepath.Join(handler.dataDir, "runs", assignment.RunID, "repo")
			if err := cleanupRunState(repoDir, assignment.RunID); err != nil {
				return fmt.Errorf("resume finishing Host Agent cleanup: %w", err)
			}
			if err := removeRepoHostLockForRun(filepath.Join(handler.dataDir, "fake-host"), assignment.HostID, assignment.RunID); err != nil {
				return fmt.Errorf("resume finishing shared Host Lock release: %w", err)
			}
			completed := assignment
			completed.State = "completed"
			if err := handler.ensureHostAgentTransition(completed); err != nil {
				return fmt.Errorf("resume completed Host Agent transition: %w", err)
			}
			continue
		}
		agent.status.RunID = lock.RunID
		if lock.State == "quarantined" || agent.status.State == "quarantined" {
			agent.status.State = "quarantined"
			assignment.State = "quarantined"
		}
		agent.takeoverNotBefore = handler.runtime.Now().Add(hostAgentHeartbeatInterval + hostAgentHeartbeatRequestTimeout + defaultBrokerCancellationWait)
		sharedRepo := filepath.Join(handler.dataDir, "fake-host")
		if err := reestablishHostAgentSharedLock(sharedRepo, assignment.HostID, assignment.RunID); err != nil {
			return fmt.Errorf("re-establish shared Host Lock for %s: %w", assignment.RunID, err)
		}
		hostIDCopy := lock.HostID
		runIDCopy := lock.RunID
		handler.assignments[lock.RunID] = &controlPlaneHostAgentAssignment{
			record:  assignment,
			repoDir: filepath.Join(handler.dataDir, "runs", lock.RunID, "repo"),
			done:    make(chan struct{}),
			sharedFakeHostRelease: func() error {
				return removeRepoHostLockForRun(sharedRepo, hostIDCopy, runIDCopy)
			},
		}
	}
	return nil
}

func validateFinishingHostAgentAssignment(assignment hostAgentAssignmentRecord) error {
	if assignment.Terminal == nil || assignment.TerminalLedger == nil || assignment.AssignedLedger == nil {
		return fmt.Errorf("terminal state is incomplete")
	}
	if err := validateHostAgentCompletionIdentity(assignment, *assignment.Terminal); err != nil {
		return err
	}
	expectedProfile := assignment.Submission.TargetProfile
	if expectedProfile == "" {
		expectedProfile = "default"
	}
	ledger := assignment.TerminalLedger
	if ledger.RunID != assignment.RunID || ledger.Scenario != assignment.Submission.Scenario || ledger.TargetProfile != expectedProfile || ledger.Host != assignment.HostID || ledger.AcceptedAt != assignment.AssignedLedger.AcceptedAt || ledger.Status != assignment.Terminal.Status {
		return fmt.Errorf("terminal Run Ledger identity does not match assignment")
	}
	if ledger.Status == resultStatusPassed && ledger.State != "completed" || ledger.Status == resultStatusFailed && ledger.State != "failed" || ledger.Status != resultStatusPassed && ledger.Status != resultStatusFailed {
		return fmt.Errorf("terminal Run Ledger state does not match status")
	}
	return nil
}

func reestablishHostAgentSharedLock(repoDir string, hostID string, runID string) error {
	if err := validateHostID(hostID); err != nil {
		return err
	}
	if err := validateRunID(runID); err != nil {
		return err
	}
	lockDir := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts")
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	return withLocalHostSideMutex(lockDir, hostID, func() error {
		lockPath := filepath.Join(lockDir, hostID+".lock")
		content, readErr := os.ReadFile(lockPath)
		created := false
		if errors.Is(readErr, os.ErrNotExist) {
			_, locked, err := acquireHostLockAtDir(lockDir, hostID)
			if err != nil {
				return err
			}
			if locked {
				return fmt.Errorf("shared Host Lock is owned by another process")
			}
			created = true
		} else if readErr != nil {
			return readErr
		} else {
			owner := parseHostLockOwner(string(content))
			if owner.ActiveRun != runID {
				return fmt.Errorf("shared Host Lock belongs to another run")
			}
			processID := ""
			for _, line := range strings.Split(string(content), "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.TrimSpace(key) == "pid" {
					processID = strings.TrimSpace(value)
					break
				}
			}
			if processID == "" {
				return fmt.Errorf("shared Host Lock has no process owner")
			}
			if processID != fmt.Sprintf("%d", os.Getpid()) {
				stale, err := isStaleHostLock(lockPath)
				if err != nil {
					return err
				}
				if !stale {
					return fmt.Errorf("shared Host Lock is owned by another live process")
				}
			}
		}
		active := fmt.Sprintf("host: %s\npid: %d\nactiveRun: %s\nauthoritativeHostLock: false\n", hostID, os.Getpid(), runID)
		if err := replaceHostLockOwnerAtDir(lockDir, hostID, active); err != nil {
			if created {
				return errors.Join(err, os.Remove(lockPath))
			}
			return err
		}
		return nil
	})
}

func (handler *controlPlaneHandler) recoverHostAgentQuarantineMarkers() error {
	for agentID, agent := range handler.hostAgents {
		path := handler.hostAgentQuarantinePath(agentID)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		var record hostAgentAssignmentRecord
		if err := readPrivateJSON(path, &record); err != nil {
			return err
		}
		if record.Version != hostAgentAPIVersion || record.State != "quarantined" || record.AgentID != agentID || record.HostID != agent.enrollment.HostID || validateRunID(record.RunID) != nil || record.LockToken == "" || record.AssignedLedger == nil {
			return fmt.Errorf("invalid Windows Host Agent quarantine marker %s", agentID)
		}
		repoDir := filepath.Join(handler.dataDir, "runs", record.RunID, "repo")
		sharedRepo := filepath.Join(handler.dataDir, "fake-host")
		assignment := &controlPlaneHostAgentAssignment{
			record: record, repoDir: repoDir, done: make(chan struct{}),
			sharedFakeHostRelease: func() error {
				return removeRepoHostLockForRun(sharedRepo, record.HostID, record.RunID)
			},
		}
		handler.assignments[record.RunID] = assignment
		agent.status.State = "quarantined"
		agent.status.RunID = record.RunID
		agent.status.SessionID = ""
		if _, _, err := handler.quarantineHostAgentAssignment(assignment, agent, "Control Plane restarted during an incomplete Windows Host Agent assignment transition"); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := syncRunLedgerDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return nil
}

func (handler *controlPlaneHandler) recoverHostAgentTransactions() error {
	root := filepath.Join(handler.dataDir, "assignment-transactions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return fmt.Errorf("invalid Host Agent transaction path %s", entry.Name())
		}
		var record hostAgentAssignmentRecord
		path := filepath.Join(root, entry.Name())
		if err := readPrivateJSON(path, &record); err != nil {
			return err
		}
		agent := handler.hostAgents[record.AgentID]
		if record.Version != hostAgentAPIVersion || validateRunID(record.RunID) != nil || entry.Name() != record.RunID+".json" || agent == nil || agent.enrollment.HostID != record.HostID || record.LockToken == "" || record.State != "assigned" && record.State != "confirmed" && record.State != "finishing" && record.State != "quarantined" && record.State != "completed" {
			return fmt.Errorf("invalid Host Agent transaction %s", entry.Name())
		}
		if agent.status.State == "quarantined" && agent.status.RunID == record.RunID && record.State != "completed" {
			terminalLedger, err := readRunLedgerRecord(filepath.Join(handler.dataDir, "runs", record.RunID, "repo"), record.RunID)
			if err != nil {
				return fmt.Errorf("recover quarantined Host Agent transaction: %w", err)
			}
			targetProfile := record.Submission.TargetProfile
			if targetProfile == "" {
				targetProfile = "default"
			}
			terminal := runCommandJSON{
				Version: controlPlaneAPIVersion, Kind: "run", Accepted: true, RunID: record.RunID,
				Scenario: record.Submission.Scenario, TargetProfile: targetProfile, Host: record.HostID,
				Status: terminalLedger.Status, StopPolicy: "unresolved",
			}
			record.State = "quarantined"
			record.AssignedLedger = &terminalLedger
			record.TerminalLedger = &terminalLedger
			record.Terminal = &terminal
		}
		if err := handler.applyHostAgentTransition(record); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := syncRunLedgerDirectory(root); err != nil {
			return err
		}
	}
	return nil
}

func loadAcceptedHostAgentRun(repoDir string, record hostAgentAssignmentRecord, runtime runRuntime) (*freshRunLifecycle, error) {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	manifest, stateDir, found, err := readStopRunManifest(repoDir, record.RunID)
	if err != nil || !found {
		return nil, errors.Join(fmt.Errorf("accepted Host Agent run manifest not found"), err)
	}
	workspace, err := newRunWorkspace(repoDir, record.RunID, "", evidenceStandaloneResultName)
	if err != nil {
		return nil, err
	}
	context := runContext{
		RepoDir: repoDir, RunWorkspace: workspace, StateDir: stateDir, EvidenceDir: workspace.EvidenceDir(),
		Workspace: workspace.LocalWorkspace(), EventsPath: workspace.EventsPath(), LogPath: workspace.LogPath(),
		ScenarioResultPath: workspace.LocalScenarioResultPath(),
		Environment:        map[string]string{scenarioResultEnvVar: workspace.LocalScenarioResultPath()},
	}
	policy := defaultRunLedgerPolicy()
	if available, ok := availableRunLedgerPolicy(repoDir); ok {
		policy = available
	}
	acceptedAt := runtime.Now()
	if ledger, ledgerErr := readRunLedgerRecord(repoDir, record.RunID); ledgerErr == nil {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, ledger.AcceptedAt); parseErr == nil {
			acceptedAt = parsed
		}
	}
	targetProfile := record.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	return &freshRunLifecycle{
		repoDir: repoDir, options: runOptions{
			ScenarioName: record.Submission.Scenario, TargetProfile: targetProfile, HostPin: record.HostID,
			StopAfter: record.Submission.StopAfter, AssignedRunID: record.RunID,
		}, runtime: runtime, context: context, manifest: manifest, accepted: true, acceptedAt: acceptedAt,
		stopPolicy: "stopped", ledgerPolicy: policy,
	}, nil
}

func readPrivateJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("private state path %s must be a regular file, not a symlink", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("private state path %s contains trailing data", path)
	}
	return nil
}

func (handler *controlPlaneHandler) hasHostAgentEnrollments() bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return len(handler.hostAgents) > 0
}

func (handler *controlPlaneHandler) serveHostAgentAPI(response http.ResponseWriter, request *http.Request) bool {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "host-agents" {
		return false
	}
	if len(parts) == 3 && parts[2] == "enroll" {
		if !handler.authorizedOperator(request) {
			writeControlPlaneError(response, http.StatusUnauthorized, "authentication required")
			return true
		}
		if request.Method != http.MethodPost {
			http.NotFound(response, request)
			return true
		}
		handler.serveHostAgentEnrollment(response, request)
		return true
	}
	if len(parts) == 4 && parts[3] == "status" {
		if !handler.authorizedOperator(request) {
			writeControlPlaneError(response, http.StatusUnauthorized, "authentication required")
			return true
		}
		if request.Method != http.MethodGet {
			http.NotFound(response, request)
			return true
		}
		handler.serveHostAgentStatus(response, parts[2])
		return true
	}
	if len(parts) < 4 || !handler.authorizedHostAgent(request, parts[2]) {
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication required")
		return true
	}
	switch {
	case len(parts) == 4 && parts[3] == "register" && request.Method == http.MethodPost:
		handler.serveHostAgentRegistration(response, request, parts[2])
	case len(parts) == 4 && parts[3] == "heartbeat" && request.Method == http.MethodPost:
		handler.serveHostAgentHeartbeat(response, request, parts[2])
	case len(parts) == 5 && parts[3] == "assignments" && parts[4] == "next" && request.Method == http.MethodPost:
		handler.serveHostAgentNextAssignment(response, request, parts[2])
	case len(parts) == 6 && parts[3] == "assignments" && parts[5] == "confirm" && request.Method == http.MethodPost:
		handler.serveHostAgentConfirm(response, request, parts[2], parts[4])
	case len(parts) == 6 && parts[3] == "assignments" && parts[5] == "complete" && request.Method == http.MethodPost:
		handler.serveHostAgentComplete(response, request, parts[2], parts[4])
	case len(parts) == 6 && parts[3] == "assignments" && parts[5] == "fail" && request.Method == http.MethodPost:
		handler.serveHostAgentFail(response, request, parts[2], parts[4])
	default:
		http.NotFound(response, request)
	}
	return true
}

func (handler *controlPlaneHandler) authorizedOperator(request *http.Request) bool {
	provided, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	return bearer && len(provided) == len(handler.token) && subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) == 1
}

func (handler *controlPlaneHandler) authorizedHostAgent(request *http.Request, agentID string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	agent := handler.hostAgents[agentID]
	return requestMatchesHostAgentCredential(request, agent)
}

func requestMatchesHostAgentCredential(request *http.Request, agent *controlPlaneHostAgent) bool {
	if agent == nil {
		return false
	}
	provided, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !bearer || len(provided) == 0 || len(provided) > maximumHostAgentCredentialBytes {
		return false
	}
	digest := sha256.Sum256([]byte(provided))
	want, err := hex.DecodeString(agent.enrollment.CredentialSHA256)
	return err == nil && len(want) == len(digest) && subtle.ConstantTimeCompare(digest[:], want) == 1
}

func decodeHostAgentRequest(request *http.Request, target any) error {
	return decodeBoundedControlPlaneJSON(request.Body, maximumControlPlaneSubmissionBytes, target)
}

func (handler *controlPlaneHandler) serveHostAgentEnrollment(response http.ResponseWriter, request *http.Request) {
	var enrollment hostAgentEnrollmentRequest
	if err := decodeHostAgentRequest(request, &enrollment); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent enrollment")
		return
	}
	if enrollment.Version != hostAgentAPIVersion || validateHostAgentID(enrollment.AgentID) != nil || validateHostAgentHostID(enrollment.HostID) != nil || len(enrollment.Credential) < minimumHostAgentCredentialBytes || len(enrollment.Credential) > maximumHostAgentCredentialBytes {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent enrollment")
		return
	}
	digest := sha256.Sum256([]byte(enrollment.Credential))
	nowTime := handler.runtime.Now()
	now := nowTime.UTC().Format(time.RFC3339Nano)
	record := hostAgentEnrollmentRecord{
		Version: hostAgentAPIVersion, AgentID: enrollment.AgentID, HostID: enrollment.HostID,
		CredentialSHA256: hex.EncodeToString(digest[:]), EnrolledAt: now,
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if existing := handler.hostAgents[enrollment.AgentID]; existing != nil && existing.status.SessionID != "" && !nowTime.Before(existing.sessionExpiresAt) {
		existing.status.SessionID = ""
		existing.sessionExpiresAt = time.Time{}
		if existing.status.RunID == "" {
			existing.status.State = "offline"
		} else {
			existing.status.State = "locked"
		}
		if err := handler.persistHostAgentStatus(existing); err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "persist expired Windows Host Agent session")
			return
		}
	}
	if existing := handler.hostAgents[enrollment.AgentID]; existing != nil && (existing.status.RunID != "" || existing.status.SessionID != "" || existing.status.State == "reserving") {
		writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent is active")
		return
	}
	for id, existing := range handler.hostAgents {
		if id != enrollment.AgentID && existing.enrollment.HostID == enrollment.HostID {
			writeControlPlaneError(response, http.StatusConflict, "Maya Host is already enrolled")
			return
		}
	}
	agentDir := filepath.Join(handler.dataDir, "host-agents", enrollment.AgentID)
	if err := ensurePrivateControlPlaneDirectory(agentDir); err != nil {
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent enrollment")
		return
	}
	status := hostAgentStatusResponse{
		Version: hostAgentAPIVersion, Kind: "host-agent-status", AgentID: enrollment.AgentID,
		HostID: enrollment.HostID, Slots: 1, State: "enrolled",
	}
	if err := writePrivateJSON(filepath.Join(agentDir, "enrollment.json"), record); err != nil {
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent enrollment")
		return
	}
	if err := writePrivateJSON(filepath.Join(agentDir, "status.json"), status); err != nil {
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent status")
		return
	}
	handler.hostAgents[enrollment.AgentID] = &controlPlaneHostAgent{enrollment: record, status: status, notify: make(chan struct{})}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(response).Encode(status)
}

func (handler *controlPlaneHandler) serveHostAgentStatus(response http.ResponseWriter, agentID string) {
	handler.mu.Lock()
	agent := handler.hostAgents[agentID]
	if agent == nil {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusNotFound, "Windows Host Agent not found")
		return
	}
	if agent.status.SessionID != "" && !handler.runtime.Now().Before(agent.sessionExpiresAt) {
		assignment := handler.assignments[agent.status.RunID]
		if assignment != nil && assignment.record.State == "confirmed" && !assignment.finishing {
			if _, _, err := handler.quarantineHostAgentAssignment(assignment, agent, "Windows Host Agent process-session lease expired before Maya shutdown was verified"); err != nil {
				handler.mu.Unlock()
				writeControlPlaneError(response, http.StatusInternalServerError, "persist expired Windows Host Agent quarantine")
				return
			}
		} else {
			agent.status.SessionID = ""
			agent.sessionExpiresAt = time.Time{}
			if agent.status.RunID == "" {
				agent.status.State = "offline"
			} else {
				agent.status.State = "locked"
			}
			_ = handler.persistHostAgentStatus(agent)
		}
	}
	status := agent.status
	handler.mu.Unlock()
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(status)
}

func (handler *controlPlaneHandler) serveHostAgentRegistration(response http.ResponseWriter, request *http.Request, agentID string) {
	var registration hostAgentRegistrationRequest
	if err := decodeHostAgentRequest(request, &registration); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent registration")
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	agent := handler.hostAgents[agentID]
	if !requestMatchesHostAgentCredential(request, agent) {
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
		return
	}
	if agent == nil || registration.Version != hostAgentAPIVersion || registration.AgentID != agentID || registration.HostID != agent.enrollment.HostID || registration.Slots != 1 {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent registration")
		return
	}
	now := handler.runtime.Now()
	if agent.status.State == "quarantined" {
		writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent assignment is quarantined")
		return
	}
	if agent.status.RunID != "" {
		if assignment := handler.assignments[agent.status.RunID]; assignment != nil {
			if assignment.record.State == "quarantined" {
				writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent assignment is quarantined")
				return
			}
			if assignment.record.State == "finishing" {
				writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent cleanup is still committing")
				return
			}
			if assignment.finishing {
				writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent completion is still committing")
				return
			}
			sessionExpired := agent.status.SessionID != "" && !now.Before(agent.sessionExpiresAt)
			restartGraceExpired := agent.status.SessionID == "" && !now.Before(agent.takeoverNotBefore)
			if assignment.record.State == "confirmed" && !assignment.finishing && (sessionExpired || restartGraceExpired) {
				if _, _, err := handler.quarantineHostAgentAssignment(assignment, agent, "Windows Host Agent process disappeared before Maya session shutdown was verified"); err != nil {
					writeControlPlaneError(response, http.StatusInternalServerError, "persist quarantined Windows Host Agent assignment")
					return
				}
				writeControlPlaneError(response, http.StatusConflict, "confirmed Windows Host Agent assignment quarantined after process loss")
				return
			}
		}
	}
	if agent.status.RunID != "" && agent.status.SessionID == "" && now.Before(agent.takeoverNotBefore) {
		writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent recovery grace is still active")
		return
	}
	if agent.status.SessionID != "" && now.Before(agent.sessionExpiresAt) {
		writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent already has an active process")
		return
	}
	sessionID, err := newHostAgentLockToken()
	if err != nil {
		writeControlPlaneError(response, http.StatusInternalServerError, "create Windows Host Agent session")
		return
	}
	candidate := agent.status
	candidate.SessionID = sessionID
	if candidate.RunID == "" {
		candidate.State = "ready"
	} else {
		candidate.State = "locked"
	}
	statusPath := filepath.Join(handler.dataDir, "host-agents", candidate.AgentID, "status.json")
	if err := writePrivateJSON(statusPath, candidate); err != nil {
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent registration")
		return
	}
	agent.status = candidate
	agent.sessionExpiresAt = now.Add(hostAgentSessionLease)
	agent.takeoverNotBefore = time.Time{}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(agent.status)
}

func (handler *controlPlaneHandler) serveHostAgentHeartbeat(response http.ResponseWriter, request *http.Request, agentID string) {
	var heartbeat hostAgentNextRequest
	if err := decodeHostAgentRequest(request, &heartbeat); err != nil || heartbeat.Version != hostAgentAPIVersion || heartbeat.SessionID == "" {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent heartbeat")
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	agent := handler.hostAgents[agentID]
	if !requestMatchesHostAgentCredential(request, agent) {
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
		return
	}
	if !handler.acceptHostAgentSession(agent, heartbeat.SessionID) {
		writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent session changed")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(agent.status)
}

func (handler *controlPlaneHandler) touchHostAgentSession(agent *controlPlaneHostAgent) {
	agent.sessionExpiresAt = handler.runtime.Now().Add(hostAgentSessionLease)
}

func (handler *controlPlaneHandler) acceptHostAgentSession(agent *controlPlaneHostAgent, sessionID string) bool {
	if agent == nil || !sameLockToken(sessionID, agent.status.SessionID) {
		return false
	}
	if !handler.runtime.Now().Before(agent.sessionExpiresAt) {
		agent.status.SessionID = ""
		agent.sessionExpiresAt = time.Time{}
		if agent.status.RunID == "" {
			agent.status.State = "offline"
		} else {
			agent.status.State = "locked"
		}
		_ = handler.persistHostAgentStatus(agent)
		return false
	}
	handler.touchHostAgentSession(agent)
	return true
}

func (handler *controlPlaneHandler) serveHostAgentNextAssignment(response http.ResponseWriter, request *http.Request, agentID string) {
	var next hostAgentNextRequest
	if err := decodeHostAgentRequest(request, &next); err != nil || next.Version != hostAgentAPIVersion || next.SessionID == "" {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent poll")
		return
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		handler.mu.Lock()
		agent := handler.hostAgents[agentID]
		if !requestMatchesHostAgentCredential(request, agent) {
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
			return
		}
		if agent == nil {
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusNotFound, "Windows Host Agent not found")
			return
		}
		if !handler.acceptHostAgentSession(agent, next.SessionID) {
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusConflict, "Windows Host Agent session changed")
			return
		}
		if agent.status.RunID != "" {
			assignment := handler.assignments[agent.status.RunID]
			if assignment != nil && (assignment.record.State == "assigned" || assignment.record.State == "confirmed") {
				result := assignmentResponse(assignment.record)
				handler.mu.Unlock()
				response.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(response).Encode(result)
				return
			}
		}
		notify := agent.notify
		handler.mu.Unlock()
		select {
		case <-notify:
			continue
		case <-timer.C:
			response.WriteHeader(http.StatusNoContent)
			return
		case <-request.Context().Done():
			handler.disconnectHostAgentSession(agentID, next.SessionID)
			return
		}
	}
}

func (handler *controlPlaneHandler) disconnectHostAgentSession(agentID string, sessionID string) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	agent := handler.hostAgents[agentID]
	if agent != nil && agent.status.RunID == "" && sameLockToken(sessionID, agent.status.SessionID) {
		agent.status.State = "offline"
		agent.status.SessionID = ""
		agent.sessionExpiresAt = time.Time{}
		_ = handler.persistHostAgentStatus(agent)
	}
}

func assignmentResponse(record hostAgentAssignmentRecord) hostAgentAssignmentResponse {
	return hostAgentAssignmentResponse{
		Version: hostAgentAPIVersion, Kind: "host-agent-assignment", RunID: record.RunID,
		AgentID: record.AgentID, HostID: record.HostID, LockToken: record.LockToken, Submission: record.Submission,
	}
}

func (handler *controlPlaneHandler) serveHostAgentConfirm(response http.ResponseWriter, request *http.Request, agentID string, runID string) {
	if validateRunID(runID) != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Host Agent Run ID")
		return
	}
	var confirmation hostAgentLockRequest
	if err := decodeHostAgentRequest(request, &confirmation); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Host Lock confirmation")
		return
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	assignment := handler.assignments[runID]
	agent := handler.hostAgents[agentID]
	if !requestMatchesHostAgentCredential(request, agent) {
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
		return
	}
	if confirmation.Version != hostAgentAPIVersion || confirmation.RunID != runID || assignment == nil || assignment.record.AgentID != agentID || agent == nil || !sameLockToken(confirmation.LockToken, assignment.record.LockToken) || assignment.record.State != "assigned" && assignment.record.State != "confirmed" || !handler.acceptHostAgentSession(agent, confirmation.SessionID) {
		writeControlPlaneError(response, http.StatusConflict, "Host Lock ownership changed")
		return
	}
	previousState := assignment.record.State
	assignment.record.State = "confirmed"
	agent.status.State = "running"
	if err := handler.ensureHostAgentTransition(assignment.record); err != nil {
		assignment.record.State = previousState
		agent.status.State = "locked"
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Host Lock confirmation")
		return
	}
	_ = handler.persistHostAgentStatus(agent)
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(agent.status)
}

func (handler *controlPlaneHandler) serveHostAgentComplete(response http.ResponseWriter, request *http.Request, agentID string, runID string) {
	if validateRunID(runID) != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Host Agent Run ID")
		return
	}
	var completion hostAgentCompletionRequest
	if err := decodeHostAgentRequest(request, &completion); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent completion")
		return
	}
	handler.mu.Lock()
	assignment := handler.assignments[runID]
	agent := handler.hostAgents[agentID]
	if !requestMatchesHostAgentCredential(request, agent) {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
		return
	}
	if assignment == nil {
		handler.mu.Unlock()
		var completed hostAgentAssignmentRecord
		err := readPrivateJSON(filepath.Join(handler.dataDir, "assignments", runID+".json"), &completed)
		if err == nil && completed.State == "completed" && completed.AgentID == agentID && sameLockToken(completion.LockToken, completed.LockToken) && completed.Terminal != nil {
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(completed.Terminal)
			return
		}
		writeControlPlaneError(response, http.StatusConflict, "Host Lock ownership changed")
		return
	}
	if assignment.record.State == "finishing" {
		if completion.Version != hostAgentAPIVersion || completion.RunID != runID || assignment.record.AgentID != agentID || agent == nil || !sameLockToken(completion.LockToken, assignment.record.LockToken) || !handler.acceptHostAgentSession(agent, completion.SessionID) {
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusConflict, "Host Lock ownership changed")
			return
		}
		terminal, err := handler.resumeFinishingHostAgentAssignment(assignment, agent)
		handler.mu.Unlock()
		if err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "resume Windows Host Agent completion")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(terminal)
		return
	}
	if completion.Version != hostAgentAPIVersion || completion.RunID != runID || assignment == nil || assignment.record.AgentID != agentID || agent == nil || !sameLockToken(completion.LockToken, assignment.record.LockToken) || assignment.record.State != "confirmed" || assignment.finishing || !handler.acceptHostAgentSession(agent, completion.SessionID) {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusConflict, "Host Lock ownership changed")
		return
	}
	assignment.finishing = true
	handler.mu.Unlock()

	outcome, originalLedger, terminalLedger, err := handler.acceptHostAgentCompletion(assignment, completion)
	if err != nil {
		handler.mu.Lock()
		if current := handler.assignments[runID]; current == assignment {
			assignment.finishing = false
		}
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent completion")
		return
	}
	assignment.originalLedger = &originalLedger
	assignment.terminalLedger = &terminalLedger
	if err := handler.finishHostAgentAssignment(assignment, completion.SessionID, outcome, nil); err != nil {
		_ = handler.resetHostAgentFinishing(assignment)
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent completion")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(controlPlaneTerminalResponse(outcome, nil, assignment.repoDir))
}

func (handler *controlPlaneHandler) serveHostAgentFail(response http.ResponseWriter, request *http.Request, agentID string, runID string) {
	if validateRunID(runID) != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Host Agent Run ID")
		return
	}
	var failure hostAgentFailureRequest
	if err := decodeHostAgentRequest(request, &failure); err != nil || failure.Version != hostAgentAPIVersion || failure.RunID != runID || failure.Diagnostic == "" || len(failure.Diagnostic) > 512 {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Windows Host Agent failure")
		return
	}
	handler.mu.Lock()
	assignment := handler.assignments[runID]
	agent := handler.hostAgents[agentID]
	if !requestMatchesHostAgentCredential(request, agent) {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusUnauthorized, "Windows Host Agent authentication changed")
		return
	}
	if assignment == nil || assignment.record.AgentID != agentID || agent == nil || !sameLockToken(failure.LockToken, assignment.record.LockToken) || assignment.record.State != "confirmed" || assignment.finishing || !handler.acceptHostAgentSession(agent, failure.SessionID) {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusConflict, "Host Lock ownership changed")
		return
	}
	if failure.Quarantine {
		outcome, runErr, err := handler.quarantineHostAgentAssignment(assignment, agent, failure.Diagnostic)
		if err != nil {
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusInternalServerError, "persist quarantined Windows Host Agent assignment")
			return
		}
		handler.mu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(controlPlaneTerminalResponse(outcome, runErr, assignment.repoDir))
		return
	}
	assignment.finishing = true
	handler.mu.Unlock()

	runErr := errors.New(failure.Diagnostic)
	run := assignment.run
	if run == nil {
		var err error
		run, err = loadAcceptedHostAgentRun(assignment.repoDir, assignment.record, handler.runtime)
		if err != nil {
			handler.mu.Lock()
			assignment.finishing = false
			handler.mu.Unlock()
			writeControlPlaneError(response, http.StatusInternalServerError, "load durable Windows Host Agent run")
			return
		}
		assignment.run = run
	}
	originalLedger, err := readRunLedgerRecord(run.repoDir, run.manifest.RunID)
	if err != nil {
		_ = handler.resetHostAgentFinishing(assignment)
		writeControlPlaneError(response, http.StatusInternalServerError, "read durable Windows Host Agent run")
		return
	}
	targetProfile := assignment.record.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	run.host.HostID = assignment.record.HostID
	run.host.TargetProfile = targetProfile
	run.manifest.Host = assignment.record.HostID
	run.manifest.TargetProfile = targetProfile
	run.failedLayer = failureLayerRunState
	outcome, finishErr := run.finishEarlyFailure(runErr)
	ledgerErr := finalizeRunLedger(run.repoDir, outcome, run.manifest, run.ledgerPolicy, run.runtime.Now())
	runErr = errors.Join(finishErr, ledgerErr)
	terminalLedger, terminalErr := readRunLedgerRecord(run.repoDir, run.manifest.RunID)
	restoreErr := writeRunLedgerRecord(run.repoDir, originalLedger)
	if terminalErr != nil || restoreErr != nil {
		_ = handler.resetHostAgentFinishing(assignment)
		writeControlPlaneError(response, http.StatusInternalServerError, "stage Windows Host Agent failure")
		return
	}
	assignment.originalLedger = &originalLedger
	assignment.terminalLedger = &terminalLedger
	if err := handler.finishHostAgentAssignment(assignment, failure.SessionID, outcome, runErr); err != nil {
		_ = handler.resetHostAgentFinishing(assignment)
		writeControlPlaneError(response, http.StatusInternalServerError, "persist Windows Host Agent failure")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(controlPlaneTerminalResponse(outcome, runErr, assignment.repoDir))
}

func (handler *controlPlaneHandler) quarantineHostAgentAssignment(assignment *controlPlaneHostAgentAssignment, agent *controlPlaneHostAgent, diagnostic string) (runOutcome, error, error) {
	runErr := errors.New(diagnostic)
	run := assignment.run
	if run == nil {
		var err error
		run, err = loadAcceptedHostAgentRun(assignment.repoDir, assignment.record, handler.runtime)
		if err != nil {
			return runOutcome{}, runErr, err
		}
		assignment.run = run
	}
	targetProfile := assignment.record.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	run.host.HostID = assignment.record.HostID
	run.host.TargetProfile = targetProfile
	run.manifest.Host = assignment.record.HostID
	run.manifest.TargetProfile = targetProfile
	run.stopPolicy = "unresolved"
	if err := run.ensureAcceptedFailureEvidence(runErr); err != nil {
		return runOutcome{}, runErr, err
	}
	run.failure.CleanupState = "failed"
	run.failure.RemediationHint = "Verify and stop the retained Maya session; this version keeps the quarantined Host Lock fail-closed."
	if err := run.updateTerminalFailureEvidence(); err != nil {
		return runOutcome{}, runErr, err
	}
	outcome := run.currentOutcome()
	if err := finalizeRunLedger(run.repoDir, outcome, run.manifest, run.ledgerPolicy, run.runtime.Now()); err != nil {
		return runOutcome{}, runErr, err
	}
	terminalLedger, err := readRunLedgerRecord(run.repoDir, assignment.record.RunID)
	if err != nil {
		return runOutcome{}, runErr, err
	}
	quarantined := assignment.record
	quarantined.State = "quarantined"
	quarantined.AssignedLedger = &terminalLedger
	quarantined.TerminalLedger = &terminalLedger
	terminal := controlPlaneTerminalResponse(outcome, runErr, assignment.repoDir)
	quarantined.Terminal = &terminal
	if err := handler.ensureHostAgentTransition(quarantined); err != nil {
		return runOutcome{}, runErr, err
	}
	assignment.record = quarantined
	assignment.outcome = outcome
	assignment.err = runErr
	assignment.terminalLedger = &terminalLedger
	if !assignment.finished {
		assignment.finished = true
		close(assignment.done)
	}
	agent.status.State = "quarantined"
	agent.status.SessionID = ""
	agent.sessionExpiresAt = time.Time{}
	_ = handler.persistHostAgentStatus(agent)
	handler.signalHostAgent(agent)
	return outcome, runErr, nil
}

func (handler *controlPlaneHandler) resetHostAgentFinishing(assignment *controlPlaneHostAgentAssignment) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.assignments[assignment.record.RunID] == assignment && assignment.record.State == "confirmed" {
		if assignment.originalLedger != nil {
			if err := writeRunLedgerRecord(assignment.repoDir, *assignment.originalLedger); err != nil {
				return err
			}
		}
		assignment.finishing = false
	}
	return nil
}

func (handler *controlPlaneHandler) finishHostAgentAssignment(assignment *controlPlaneHostAgentAssignment, sessionID string, outcome runOutcome, runErr error) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	current := handler.assignments[assignment.record.RunID]
	if current != assignment || assignment.record.State != "confirmed" || !assignment.finishing {
		return fmt.Errorf("Host Lock ownership changed")
	}
	agent := handler.hostAgents[assignment.record.AgentID]
	if !handler.acceptHostAgentSession(agent, sessionID) {
		return fmt.Errorf("Windows Host Agent session changed before completion commit")
	}
	finishing := assignment.record
	finishing.State = "finishing"
	terminal := controlPlaneTerminalResponse(outcome, runErr, assignment.repoDir)
	finishing.Terminal = &terminal
	finishing.TerminalLedger = assignment.terminalLedger
	finishing.AssignedLedger = assignment.terminalLedger
	if err := handler.ensureHostAgentTransition(finishing); err != nil {
		return err
	}
	assignment.record = finishing
	if cleanupErr := cleanupRunState(assignment.repoDir, assignment.record.RunID); cleanupErr != nil {
		return handler.quarantineHostAgentCleanupFailure(assignment, agent, outcome, cleanupErr)
	}
	if assignment.sharedFakeHostRelease != nil {
		if sharedLockErr := assignment.sharedFakeHostRelease(); sharedLockErr != nil {
			return handler.quarantineHostAgentCleanupFailure(assignment, agent, outcome, sharedLockErr)
		}
		assignment.sharedFakeHostRelease = nil
	}
	completed := finishing
	completed.State = "completed"
	if err := handler.ensureHostAgentTransition(completed); err != nil {
		return err
	}
	assignment.record = completed
	assignment.outcome = outcome
	assignment.err = runErr
	assignment.finishing = false
	assignment.finished = true
	agent.status.State = "offline"
	agent.status.RunID = ""
	agent.status.SessionID = ""
	agent.sessionExpiresAt = time.Time{}
	_ = handler.persistHostAgentStatus(agent)
	close(assignment.done)
	delete(handler.assignments, assignment.record.RunID)
	handler.signalHostAgent(agent)
	return nil
}

func (handler *controlPlaneHandler) resumeFinishingHostAgentAssignment(assignment *controlPlaneHostAgentAssignment, agent *controlPlaneHostAgent) (runCommandJSON, error) {
	if assignment.record.Terminal == nil || assignment.record.TerminalLedger == nil {
		return runCommandJSON{}, fmt.Errorf("finishing Host Agent assignment has no terminal state")
	}
	if err := cleanupRunState(assignment.repoDir, assignment.record.RunID); err != nil {
		return runCommandJSON{}, err
	}
	if assignment.sharedFakeHostRelease != nil {
		if err := assignment.sharedFakeHostRelease(); err != nil {
			return runCommandJSON{}, err
		}
		assignment.sharedFakeHostRelease = nil
	} else if err := removeRepoHostLockForRun(filepath.Join(handler.dataDir, "fake-host"), assignment.record.HostID, assignment.record.RunID); err != nil {
		return runCommandJSON{}, err
	}
	completed := assignment.record
	completed.State = "completed"
	if err := handler.ensureHostAgentTransition(completed); err != nil {
		return runCommandJSON{}, err
	}
	assignment.record = completed
	assignment.outcome = runOutcomeFromCommandJSON(*completed.Terminal)
	assignment.err = nil
	assignment.finishing = false
	if !assignment.finished {
		assignment.finished = true
		close(assignment.done)
	}
	agent.status.State = "offline"
	agent.status.RunID = ""
	agent.status.SessionID = ""
	agent.sessionExpiresAt = time.Time{}
	_ = handler.persistHostAgentStatus(agent)
	delete(handler.assignments, assignment.record.RunID)
	handler.signalHostAgent(agent)
	return *completed.Terminal, nil
}

func (handler *controlPlaneHandler) quarantineHostAgentCleanupFailure(assignment *controlPlaneHostAgentAssignment, agent *controlPlaneHostAgent, outcome runOutcome, cleanupErr error) error {
	failure := &runFailureEvidence{
		FailedLayer: string(failureLayerRunState), Diagnostic: cleanupErr.Error(),
		RemediationHint: "Inspect cleanup state; this version keeps the quarantined Host Lock fail-closed.",
		CaptureState:    "completed", CleanupState: "failed",
	}
	outcome.Result = ScenarioResult{Status: resultStatusFailed, Summary: cleanupErr.Error()}
	outcome.StopPolicy = "unresolved"
	outcome.FollowUpCommands = nil
	outcome.Failure = failure
	ledger := *assignment.terminalLedger
	ledger.State = "cleanup-failed"
	ledger.Status = resultStatusFailed
	ledger.UpdatedAt = handler.runtime.Now().UTC().Format(time.RFC3339Nano)
	ledger.CompletedAt = ledger.UpdatedAt
	if err := writeControlPlaneCleanupFailureEvidence(assignment.repoDir, assignment.record.RunID, failure); err != nil {
		return errors.Join(cleanupErr, err)
	}
	quarantined := assignment.record
	quarantined.State = "quarantined"
	quarantined.AssignedLedger = &ledger
	quarantined.TerminalLedger = &ledger
	terminal := controlPlaneTerminalResponse(outcome, cleanupErr, assignment.repoDir)
	quarantined.Terminal = &terminal
	if err := handler.ensureHostAgentTransition(quarantined); err != nil {
		return errors.Join(cleanupErr, err)
	}
	assignment.record = quarantined
	assignment.outcome = outcome
	assignment.err = cleanupErr
	assignment.finishing = false
	assignment.finished = true
	assignment.terminalLedger = &ledger
	agent.status.State = "quarantined"
	agent.status.SessionID = ""
	agent.sessionExpiresAt = time.Time{}
	_ = handler.persistHostAgentStatus(agent)
	close(assignment.done)
	handler.signalHostAgent(agent)
	return cleanupErr
}

func writeControlPlaneCleanupFailureEvidence(repoDir string, runID string, failure *runFailureEvidence) error {
	path := filepath.Join(repoDir, "artifacts", "maya-stall", runID, evidenceBundleFileName)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return err
	}
	bundle.Status = resultStatusFailed
	bundle.Failure = failure
	bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
	return writeJSONFile(path, bundle)
}

func sameLockToken(got string, want string) bool {
	return got != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (handler *controlPlaneHandler) runScenarioThroughHostAgent(repoDir string, submission controlPlaneSubmission, options runOptions, runtime runRuntime) (runOutcome, error) {
	if submission.StopAfter != stopAfterAlways {
		return failHostAgentSelection(repoDir, options, runtime, errors.New("registered Windows Host Agent runs require stop-after always"))
	}
	handler.mu.Lock()
	var selected *controlPlaneHostAgent
	var sharedFakeHostRelease func() error
	ids := make([]string, 0, len(handler.hostAgents))
	for id := range handler.hostAgents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		agent := handler.hostAgents[id]
		if agent.status.State == "ready" && agent.status.SessionID != "" && !handler.runtime.Now().Before(agent.sessionExpiresAt) {
			agent.status.State = "offline"
			agent.status.SessionID = ""
			agent.sessionExpiresAt = time.Time{}
			_ = handler.persistHostAgentStatus(agent)
			continue
		}
		if agent.status.State == "ready" && agent.status.RunID == "" && agent.status.SessionID != "" && agent.status.Slots == 1 {
			release, locked, err := acquireHostLock(filepath.Join(handler.dataDir, "fake-host"), agent.status.HostID)
			if err != nil || locked {
				continue
			}
			selected = agent
			sharedFakeHostRelease = release
			agent.status.State = "reserving"
			break
		}
	}
	handler.mu.Unlock()
	if selected == nil {
		return failHostAgentSelection(repoDir, options, runtime, errors.New("no registered ready Windows Host Agent is available"))
	}

	acceptedCallback := runtime.Accepted
	acceptedCheck := runtime.AcceptedCheck
	deferredAcceptanceRuntime := runtime
	deferredAcceptanceRuntime.Accepted = nil
	deferredAcceptanceRuntime.AcceptedCheck = nil
	run := newFreshRun(repoDir, options, deferredAcceptanceRuntime).(*freshRunLifecycle)
	if err := run.accept(); err != nil {
		sharedLockErr := sharedFakeHostRelease()
		if run.accepted {
			return handler.finishAcceptedHostAgentFailure(run, selected, errors.Join(err, sharedLockErr))
		}
		handler.mu.Lock()
		selected.status.State = "ready"
		handler.mu.Unlock()
		return runOutcome{}, errors.Join(err, sharedLockErr, run.cleanupUnacceptedOwnership())
	}
	failBeforeTransition := func(runErr error) (runOutcome, error) {
		return handler.finishAcceptedHostAgentFailure(run, selected, errors.Join(runErr, sharedFakeHostRelease()))
	}
	runID := run.manifest.RunID
	sharedFakeRepo := filepath.Join(handler.dataDir, "fake-host")
	if err := markHostLockActive(sharedFakeRepo, selected.status.HostID, runID); err != nil {
		return failBeforeTransition(err)
	}
	sharedFakeHostRelease = func() error {
		return removeRepoHostLockForRun(sharedFakeRepo, selected.status.HostID, runID)
	}
	lockToken, err := newHostAgentLockToken()
	if err != nil {
		return failBeforeTransition(err)
	}
	createdAt := handler.runtime.Now().UTC().Format(time.RFC3339Nano)
	assignedLedger, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return failBeforeTransition(err)
	}
	assignedLedger.Host = selected.status.HostID
	record := hostAgentAssignmentRecord{
		Version: hostAgentAPIVersion, RunID: runID, AgentID: selected.status.AgentID, HostID: selected.status.HostID,
		LockToken: lockToken, State: "assigned", CreatedAt: createdAt, Submission: submission, AssignedLedger: &assignedLedger,
	}
	targetProfile := record.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	run.host.HostID = record.HostID
	run.host.TargetProfile = targetProfile
	run.manifest.Host = record.HostID
	run.manifest.TargetProfile = targetProfile
	assignment := &controlPlaneHostAgentAssignment{record: record, repoDir: repoDir, done: make(chan struct{}), run: run, sharedFakeHostRelease: sharedFakeHostRelease}

	handler.mu.Lock()
	if handler.hostAgents[selected.status.AgentID] != selected || selected.status.State != "reserving" || selected.status.RunID != "" {
		handler.mu.Unlock()
		return failBeforeTransition(errors.New("Windows Host Agent reservation changed"))
	}
	if selected.status.SessionID == "" || !handler.runtime.Now().Before(selected.sessionExpiresAt) {
		selected.status.State = "offline"
		selected.status.SessionID = ""
		selected.sessionExpiresAt = time.Time{}
		persistErr := handler.persistHostAgentStatus(selected)
		handler.mu.Unlock()
		return failBeforeTransition(errors.Join(errors.New("Windows Host Agent process-session lease expired during reservation"), persistErr))
	}
	quarantineMarkerPath := handler.hostAgentQuarantinePath(record.AgentID)
	quarantineIntent := record
	quarantineIntent.State = "quarantined"
	if err := writePrivateJSON(quarantineMarkerPath, quarantineIntent); err != nil {
		handler.mu.Unlock()
		return failBeforeTransition(fmt.Errorf("persist Windows Host Agent assignment write-ahead marker: %w", err))
	}
	handler.mu.Unlock()
	if acceptedCallback != nil {
		acceptedCallback(run.currentOutcome())
	}
	var transitionErr error
	if acceptedCheck != nil {
		transitionErr = acceptedCheck()
	}
	handler.mu.Lock()
	if handler.hostAgents[selected.status.AgentID] != selected || selected.status.State != "reserving" || selected.status.RunID != "" {
		transitionErr = errors.Join(transitionErr, errors.New("Windows Host Agent reservation changed during acceptance"))
	}
	if selected.status.SessionID == "" || !handler.runtime.Now().Before(selected.sessionExpiresAt) {
		transitionErr = errors.Join(transitionErr, errors.New("Windows Host Agent process-session lease expired during acceptance"))
	}
	if transitionErr == nil {
		transitionErr = handler.ensureHostAgentTransition(record)
	}
	if transitionErr == nil {
		if err := os.Remove(quarantineMarkerPath); err != nil {
			transitionErr = err
		} else if err := syncRunLedgerDirectory(filepath.Dir(quarantineMarkerPath)); err != nil {
			transitionErr = err
		}
	}
	if transitionErr != nil {
		record.State = "quarantined"
		assignment.record = record
		handler.assignments[runID] = assignment
		markerErr := writePrivateJSON(quarantineMarkerPath, record)
		selected.status.State = "quarantined"
		selected.status.RunID = runID
		selected.status.SessionID = ""
		selected.sessionExpiresAt = time.Time{}
		var statusErr error
		if markerErr == nil {
			statusErr = handler.persistHostAgentStatus(selected)
		}
		handler.mu.Unlock()
		outcome, failureErr := handler.finishAcceptedHostAgentFailure(run, selected, errors.Join(transitionErr, markerErr, statusErr, errors.New("Windows Host Agent slot quarantined after incomplete assignment transition")))
		handler.mu.Lock()
		terminalLedger, terminalLedgerErr := readRunLedgerRecord(repoDir, runID)
		var quarantineTransitionErr error
		var markerCleanupErr error
		if terminalLedgerErr == nil {
			terminal := controlPlaneTerminalResponse(outcome, failureErr, repoDir)
			record.AssignedLedger = &terminalLedger
			record.TerminalLedger = &terminalLedger
			record.Terminal = &terminal
			quarantineTransitionErr = handler.ensureHostAgentTransition(record)
			if quarantineTransitionErr == nil {
				assignment.record = record
				assignment.terminalLedger = &terminalLedger
				if err := os.Remove(quarantineMarkerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					markerCleanupErr = err
				} else if err == nil {
					markerCleanupErr = syncRunLedgerDirectory(filepath.Dir(quarantineMarkerPath))
				}
			}
		}
		failureErr = errors.Join(failureErr, terminalLedgerErr, quarantineTransitionErr, markerCleanupErr)
		assignment.outcome = outcome
		assignment.err = failureErr
		assignment.finished = true
		close(assignment.done)
		handler.mu.Unlock()
		return outcome, failureErr
	}
	selected.status.State = "locked"
	selected.status.RunID = runID
	_ = handler.persistHostAgentStatus(selected)
	handler.assignments[runID] = assignment
	handler.signalHostAgent(selected)
	handler.mu.Unlock()

	for {
		handler.mu.Lock()
		if assignment.finished {
			handler.mu.Unlock()
			break
		}
		agent := handler.hostAgents[assignment.record.AgentID]
		now := handler.runtime.Now()
		sessionExpired := agent != nil && agent.status.SessionID != "" && !now.Before(agent.sessionExpiresAt)
		restartGraceExpired := agent != nil && agent.status.SessionID == "" && !now.Before(agent.takeoverNotBefore)
		if assignment.record.State == "confirmed" && !assignment.finishing && (sessionExpired || restartGraceExpired) {
			_, _, _ = handler.quarantineHostAgentAssignment(assignment, agent, "Windows Host Agent process-session lease expired before Maya shutdown was verified")
		}
		done := assignment.done
		handler.mu.Unlock()
		select {
		case <-done:
		case <-time.After(hostAgentHeartbeatInterval):
			continue
		}
		break
	}
	handler.mu.Lock()
	outcome := assignment.outcome
	runErr := assignment.err
	handler.mu.Unlock()
	return outcome, runErr
}

func failHostAgentSelection(repoDir string, options runOptions, runtime runRuntime, selectionErr error) (runOutcome, error) {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	run := &freshRunLifecycle{
		repoDir: repoDir, options: options, runtime: runtime, stopPolicy: "stopped", ledgerPolicy: defaultRunLedgerPolicy(),
	}
	if err := run.accept(); err != nil {
		return runOutcome{}, errors.Join(selectionErr, err, run.cleanupUnacceptedOwnership())
	}
	if policy, available := availableRunLedgerPolicy(repoDir); available {
		run.ledgerPolicy = policy
	}
	run.failedLayer = failureLayerHostSelection
	outcome, runErr := run.finishEarlyFailure(selectionErr)
	ledgerErr := finalizeRunLedger(run.repoDir, outcome, run.manifest, run.ledgerPolicy, run.runtime.Now())
	return outcome, errors.Join(runErr, ledgerErr)
}

func (handler *controlPlaneHandler) finishAcceptedHostAgentFailure(run *freshRunLifecycle, selected *controlPlaneHostAgent, runErr error) (runOutcome, error) {
	handler.mu.Lock()
	if selected.status.State == "reserving" {
		selected.status.State = "ready"
		_ = handler.persistHostAgentStatus(selected)
	}
	handler.mu.Unlock()
	run.failedLayer = failureLayerHostSelection
	outcome, finishErr := run.finishEarlyFailure(runErr)
	ledgerErr := finalizeRunLedger(run.repoDir, outcome, run.manifest, run.ledgerPolicy, run.runtime.Now())
	return outcome, errors.Join(finishErr, ledgerErr)
}

func newHostAgentLockToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (handler *controlPlaneHandler) signalHostAgent(agent *controlPlaneHostAgent) {
	close(agent.notify)
	agent.notify = make(chan struct{})
}

func (handler *controlPlaneHandler) persistHostAgentStatus(agent *controlPlaneHostAgent) error {
	return writePrivateJSON(filepath.Join(handler.dataDir, "host-agents", agent.status.AgentID, "status.json"), agent.status)
}

func (handler *controlPlaneHandler) persistAssignment(record hostAgentAssignmentRecord) error {
	return writePrivateJSON(filepath.Join(handler.dataDir, "assignments", record.RunID+".json"), record)
}

func (handler *controlPlaneHandler) persistHostAgentTransition(record hostAgentAssignmentRecord) error {
	transactionPath := filepath.Join(handler.dataDir, "assignment-transactions", record.RunID+".json")
	if err := writePrivateJSON(transactionPath, record); err != nil {
		return err
	}
	if err := handler.applyHostAgentTransition(record); err != nil {
		return err
	}
	if err := os.Remove(transactionPath); err != nil {
		return err
	}
	return syncRunLedgerDirectory(filepath.Dir(transactionPath))
}

func (handler *controlPlaneHandler) ensureHostAgentTransition(record hostAgentAssignmentRecord) error {
	persistErr := handler.persistHostAgentTransition(record)
	if persistErr == nil {
		return nil
	}
	recoverErr := handler.recoverHostAgentTransactions()
	verifyErr := handler.verifyHostAgentTransition(record)
	if recoverErr == nil && verifyErr == nil {
		return nil
	}
	return errors.Join(persistErr, recoverErr, verifyErr)
}

func (handler *controlPlaneHandler) verifyHostAgentTransition(want hostAgentAssignmentRecord) error {
	var assignment hostAgentAssignmentRecord
	if err := readPrivateJSON(filepath.Join(handler.dataDir, "assignments", want.RunID+".json"), &assignment); err != nil {
		return err
	}
	if assignment.State != want.State || assignment.AgentID != want.AgentID || assignment.HostID != want.HostID || !sameLockToken(assignment.LockToken, want.LockToken) {
		return fmt.Errorf("durable Host Agent assignment does not match transition")
	}
	if want.State == "completed" {
		if want.TerminalLedger == nil {
			return fmt.Errorf("completed Host Agent transition is missing its terminal Run Ledger")
		}
		live, err := readRunLedgerRecord(filepath.Join(handler.dataDir, "runs", want.RunID, "repo"), want.RunID)
		if err != nil || live.State != want.TerminalLedger.State || live.Status != want.TerminalLedger.Status || live.AcceptedAt != want.TerminalLedger.AcceptedAt {
			return errors.Join(fmt.Errorf("completed Host Agent transition did not publish its terminal Run Ledger"), err)
		}
		if _, err := os.Lstat(handler.hostLockPath(want.HostID)); !errors.Is(err, os.ErrNotExist) {
			return errors.Join(fmt.Errorf("completed Host Agent transition retained its Host Lock"), err)
		}
		return nil
	}
	var lock hostAgentLockRecord
	if err := readPrivateJSON(handler.hostLockPath(want.HostID), &lock); err != nil {
		return err
	}
	if lock.State != want.State || lock.RunID != want.RunID || lock.AgentID != want.AgentID || !sameLockToken(lock.LockToken, want.LockToken) {
		return fmt.Errorf("durable Host Lock does not match transition")
	}
	if want.AssignedLedger == nil {
		return fmt.Errorf("active Host Agent transition is missing its assigned Run Ledger")
	}
	live, err := readRunLedgerRecord(filepath.Join(handler.dataDir, "runs", want.RunID, "repo"), want.RunID)
	if err != nil || live.Host != want.HostID || live.AcceptedAt != want.AssignedLedger.AcceptedAt {
		return errors.Join(fmt.Errorf("active Host Agent transition did not publish its assigned Run Ledger"), err)
	}
	return nil
}

func (handler *controlPlaneHandler) applyHostAgentTransition(record hostAgentAssignmentRecord) error {
	if record.State == "completed" {
		if err := handler.persistAssignment(record); err != nil {
			return err
		}
		if record.TerminalLedger == nil {
			return fmt.Errorf("completed Host Agent transition is missing its terminal Run Ledger")
		}
		repoDir := filepath.Join(handler.dataDir, "runs", record.RunID, "repo")
		if err := writeRunLedgerRecord(repoDir, *record.TerminalLedger); err != nil {
			return err
		}
		if err := os.Remove(handler.hostLockPath(record.HostID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncRunLedgerDirectory(filepath.Join(handler.dataDir, "host-locks"))
	}
	if record.AssignedLedger == nil {
		return fmt.Errorf("active Host Agent transition is missing its assigned Run Ledger")
	}
	repoDir := filepath.Join(handler.dataDir, "runs", record.RunID, "repo")
	if err := writeRunLedgerRecord(repoDir, *record.AssignedLedger); err != nil {
		return err
	}
	if err := handler.persistHostLock(record, record.State); err != nil {
		return err
	}
	return handler.persistAssignment(record)
}

func (handler *controlPlaneHandler) hostLockPath(hostID string) string {
	return filepath.Join(handler.dataDir, "host-locks", hostID+".json")
}

func (handler *controlPlaneHandler) hostAgentQuarantinePath(agentID string) string {
	return filepath.Join(handler.dataDir, "host-agents", agentID, "quarantine.json")
}

func (handler *controlPlaneHandler) persistHostLock(assignment hostAgentAssignmentRecord, state string) error {
	return writePrivateJSON(handler.hostLockPath(assignment.HostID), hostAgentLockRecord{
		Version: hostAgentAPIVersion, RunID: assignment.RunID, AgentID: assignment.AgentID, HostID: assignment.HostID,
		LockToken: assignment.LockToken, State: state, CreatedAt: assignment.CreatedAt,
	})
}

func writePrivateJSON(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeRunLedgerBytes(path, append(content, '\n'))
}

func (handler *controlPlaneHandler) acceptHostAgentCompletion(assignment *controlPlaneHostAgentAssignment, completion hostAgentCompletionRequest) (runOutcome, runLedgerRecord, runLedgerRecord, error) {
	expectedProfile := assignment.record.Submission.TargetProfile
	if expectedProfile == "" {
		expectedProfile = "default"
	}
	if err := validateHostAgentCompletionIdentity(assignment.record, completion.Terminal); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("terminal result does not match assignment")
	}
	if err := validateHostAgentResultFiles(assignment.record.RunID, completion.Files); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	stagingRoot, err := os.MkdirTemp(filepath.Join(handler.dataDir, "incoming"), "."+assignment.record.RunID+"-*")
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	defer func() { _ = os.RemoveAll(stagingRoot) }()
	if err := os.Chmod(stagingRoot, 0o700); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if err := materializeControlPlaneFiles(stagingRoot, completion.Files); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	record, err := readRunLedgerRecord(stagingRoot, assignment.record.RunID)
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if record.Host != assignment.record.HostID || record.Status == resultStatusPassed && record.State != "completed" || record.Status == resultStatusFailed && record.State != "failed" || record.Status != resultStatusPassed && record.Status != resultStatusFailed {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent did not return a cleaned terminal run")
	}
	result, err := readControlPlaneResult(stagingRoot, record)
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if !result.Final || result.CleanupState != "completed" {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent cleanup is not complete")
	}
	evidence, err := readControlPlaneEvidence(stagingRoot, record)
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if evidence.Bundle.Scenario != assignment.record.Submission.Scenario || evidence.Bundle.TargetProfile != expectedProfile || evidence.Bundle.Host != assignment.record.HostID {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent Evidence Bundle changed immutable run identity")
	}
	for _, artifact := range evidence.Bundle.VisualEvidence {
		if artifact.TargetProfile != "" && artifact.TargetProfile != expectedProfile || artifact.Host != "" && artifact.Host != assignment.record.HostID {
			return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent Visual Evidence changed target identity")
		}
	}
	stagedOutcome := runOutcomeFromCommandJSON(completion.Terminal)
	if stagedOutcome.Result.Status != record.Status || result.Status != record.Status || result.Result.Status != record.Status || evidence.Bundle.Status != record.Status {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent terminal status does not match durable result")
	}
	original, err := readRunLedgerRecord(assignment.repoDir, assignment.record.RunID)
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if record.RunID != original.RunID || record.Scenario != original.Scenario || record.TargetProfile != original.TargetProfile || record.Host != original.Host {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, fmt.Errorf("Windows Host Agent changed immutable Run Ledger identity")
	}
	merged := record
	merged.Version = original.Version
	merged.RunID = original.RunID
	merged.Scenario = original.Scenario
	merged.TargetProfile = original.TargetProfile
	merged.Host = original.Host
	merged.AcceptedAt = original.AcceptedAt
	merged.EvidenceDir = original.EvidenceDir
	merged.Events = original.Events
	merged.Log = original.Log
	stagedOutcome.RunID = original.RunID
	stagedOutcome.Scenario = original.Scenario
	stagedOutcome.TargetProfile = original.TargetProfile
	stagedOutcome.Host = original.Host
	stagedOutcome.Accepted = true
	stagedOutcome.StopPolicy = "stopped"
	stagedOutcome.FollowUpCommands = nil
	policy := defaultRunLedgerPolicy()
	if available, ok := availableRunLedgerPolicy(assignment.repoDir); ok {
		policy = available
	}
	merged.EventCount = 0
	merged.EventBytes = 0
	merged.EventsOmitted = 0
	merged.EventsTruncated = false
	merged.LogBytes = 0
	merged.LogTruncated = false
	if err := syncRunLedgerArtifacts(stagingRoot, &merged, policy); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	stagedFiles, err := buildHostAgentResultFiles(stagingRoot, assignment.record.RunID)
	if err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	if err := copyHostAgentResultFiles(assignment.repoDir, stagedFiles, assignment.record.RunID); err != nil {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, err
	}
	serverResult, err := readControlPlaneResult(assignment.repoDir, merged)
	if err != nil || !serverResult.Final || serverResult.CleanupState != "completed" {
		return runOutcome{}, runLedgerRecord{}, runLedgerRecord{}, errors.Join(fmt.Errorf("transferred Windows Host Agent result is not durable"), err)
	}
	stagedOutcome.StateDir = "/v1/runs/" + assignment.record.RunID + "/status"
	stagedOutcome.EvidenceDir = "/v1/runs/" + assignment.record.RunID + "/evidence"
	return stagedOutcome, original, merged, nil
}

func validateHostAgentCompletionIdentity(assignment hostAgentAssignmentRecord, terminal runCommandJSON) error {
	expectedProfile := assignment.Submission.TargetProfile
	if expectedProfile == "" {
		expectedProfile = "default"
	}
	if terminal.Version != controlPlaneAPIVersion || terminal.Kind != "run" || !terminal.Accepted || terminal.RunID != assignment.RunID || terminal.Scenario != assignment.Submission.Scenario || terminal.TargetProfile != expectedProfile || terminal.Host != assignment.HostID || terminal.StopPolicy != "stopped" || len(terminal.FollowUpCommands) != 0 {
		return fmt.Errorf("terminal result does not match assignment")
	}
	return nil
}

func validateHostAgentResultFiles(runID string, files []controlPlaneFile) error {
	if len(files) == 0 || len(files) > 10_000 {
		return fmt.Errorf("Windows Host Agent result file count is invalid")
	}
	ledgerRoot := filepath.ToSlash(filepath.Join(".maya-stall", "state", "ledger", "runs", runID))
	evidenceRoot := filepath.ToSlash(filepath.Join("artifacts", "maya-stall", runID))
	seen := make(map[string]bool, len(files))
	kinds := make(map[string]string, len(files))
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path)))
		if file.Path == "" || filepath.IsAbs(filepath.FromSlash(file.Path)) || strings.Contains(file.Path, "\\") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("invalid Windows Host Agent result path")
		}
		if clean != file.Path || seen[clean] || file.Kind != "file" && file.Kind != "directory" || file.Kind == "directory" && len(file.Content) != 0 {
			return fmt.Errorf("invalid Windows Host Agent result path")
		}
		if clean != ledgerRoot && !strings.HasPrefix(clean, ledgerRoot+"/") && clean != evidenceRoot && !strings.HasPrefix(clean, evidenceRoot+"/") {
			return fmt.Errorf("Windows Host Agent result path is outside durable run data")
		}
		seen[clean] = true
		kinds[clean] = file.Kind
	}
	for path := range kinds {
		for parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))); parent != "."; parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent))) {
			if kinds[parent] == "file" {
				return fmt.Errorf("Windows Host Agent result file cannot contain child paths")
			}
		}
	}
	for _, required := range []string{
		filepath.ToSlash(filepath.Join(ledgerRoot, "run.json")),
		filepath.ToSlash(filepath.Join(evidenceRoot, evidenceBundleFileName)),
	} {
		if !seen[required] {
			return fmt.Errorf("Windows Host Agent result is missing %s", required)
		}
	}
	return nil
}

func materializeControlPlaneFiles(repoDir string, files []controlPlaneFile) error {
	for _, file := range files {
		path := filepath.Join(repoDir, filepath.FromSlash(file.Path))
		switch file.Kind {
		case "directory":
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case "file":
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, file.Content, 0o600); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported Windows Host Agent result file kind")
		}
	}
	return nil
}

func copyHostAgentResultFiles(repoDir string, files []controlPlaneFile, runID string) error {
	ordered := append([]controlPlaneFile(nil), files...)
	runRecord := filepath.ToSlash(filepath.Join(".maya-stall", "state", "ledger", "runs", runID, "run.json"))
	sort.SliceStable(ordered, func(left int, right int) bool {
		if ordered[left].Path == runRecord {
			return false
		}
		if ordered[right].Path == runRecord {
			return true
		}
		return ordered[left].Path < ordered[right].Path
	})
	for _, file := range ordered {
		if file.Path == runRecord {
			continue
		}
		path := filepath.Join(repoDir, filepath.FromSlash(file.Path))
		if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Dir(file.Path)); err != nil {
			return err
		}
		if file.Kind == "directory" {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := writeRunLedgerBytes(path, file.Content); err != nil {
			return err
		}
	}
	return nil
}

func runHostAgentOnce(options hostAgentRunOnceOptions, runtime runRuntime, stdout io.Writer) error {
	if err := ensureHostAgentDirectory(options.WorkRoot); err != nil {
		return err
	}
	for _, child := range []string{"runs", "host"} {
		if err := ensureHostAgentDirectory(filepath.Join(options.WorkRoot, child)); err != nil {
			return err
		}
	}
	registration := hostAgentRegistrationRequest{
		Version: hostAgentAPIVersion, AgentID: options.AgentID, HostID: options.HostID, Slots: 1,
	}
	var status hostAgentStatusResponse
	registerPath := "/v1/host-agents/" + options.AgentID + "/register"
	if err := postControlPlaneJSON(options.ControlPlane, options.CredentialEnv, registerPath, registration, runtime, http.StatusOK, &status); err != nil {
		return err
	}
	if status.SessionID == "" {
		return fmt.Errorf("Control Plane did not return a Windows Host Agent session")
	}
	options.SessionID = status.SessionID
	stopHeartbeat, heartbeatErrors, executionCancel := startHostAgentHeartbeat(options, runtime)
	defer stopHeartbeat()
	if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
		return err
	}
	assignment, err := pollHostAgentAssignment(options, runtime, heartbeatErrors)
	if err != nil {
		return err
	}
	if err := validateHostAgentAssignment(options, assignment); err != nil {
		return err
	}
	if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
		return err
	}
	lock := hostAgentLockRequest{Version: hostAgentAPIVersion, RunID: assignment.RunID, LockToken: assignment.LockToken, SessionID: options.SessionID}
	confirmPath := "/v1/host-agents/" + options.AgentID + "/assignments/" + assignment.RunID + "/confirm"
	if err := postHostAgentMutation(options, confirmPath, lock, runtime, &status); err != nil {
		return err
	}
	if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "renewing its process-session fence", err)
	}

	runRoot := filepath.Join(options.WorkRoot, "runs", assignment.RunID)
	repoDir := filepath.Join(runRoot, "repo")
	if err := os.RemoveAll(runRoot); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "cleaning a stale takeover workspace", err)
	}
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "preparing its run workspace", err)
	}
	if err := materializeControlPlaneSubmission(repoDir, assignment.Submission); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "materializing the submitted snapshot", err)
	}
	if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "renewing its process-session fence", err)
	}
	hostConfigPath, err := writeHostAgentFakeHostConfig(options, assignment)
	if err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "preparing fake Host config", err)
	}
	agentRuntime := runtime
	agentRuntime.Accepted = nil
	agentRuntime.AcceptedCheck = nil
	agentRuntime.ControlPlaneServe = nil
	agentRuntime.Host = nil
	agentRuntime.Broker = nil
	agentRuntime.ReadinessHost = nil
	agentRuntime.ReadinessBroker = nil
	agentRuntime.Cancel = executionCancel
	targetProfile := assignment.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	outcome, runErr := runScenario(repoDir, runOptions{
		ScenarioName: assignment.Submission.Scenario, TargetProfile: targetProfile, HostPin: assignment.HostID,
		HostConfig: hostConfigPath, StopAfter: assignment.Submission.StopAfter, AssignedRunID: assignment.RunID,
	}, agentRuntime)
	heartbeatErr := currentHostAgentHeartbeatError(heartbeatErrors)
	if outcome.StopPolicy == "unresolved" || outcome.Failure != nil && outcome.Failure.CleanupState == "failed" {
		return quarantineConfirmedHostAgentAssignment(options, assignment, runtime, "stopping its Maya UI Session", errors.Join(runErr, heartbeatErr))
	}
	if heartbeatErr != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "renewing its process-session fence", errors.Join(runErr, heartbeatErr))
	}
	if outcome.Host != assignment.HostID || outcome.Scenario != assignment.Submission.Scenario || outcome.TargetProfile != targetProfile {
		identityErr := fmt.Errorf("Windows Host Agent run did not reach the assigned Host identity")
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "executing the assigned Scenario", errors.Join(runErr, identityErr))
	}
	files, snapshotErr := buildHostAgentResultFilesSanitized(repoDir, assignment.RunID, []string{repoDir, options.WorkRoot})
	if snapshotErr != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "collecting durable result state", errors.Join(runErr, snapshotErr))
	}
	if err := os.RemoveAll(runRoot); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "cleaning its run workspace", errors.Join(runErr, err))
	}
	if _, err := os.Lstat(runRoot); !errors.Is(err, os.ErrNotExist) {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "verifying run workspace cleanup", errors.Join(runErr, err))
	}
	if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
		return failConfirmedHostAgentAssignment(options, assignment, runtime, "renewing its process-session fence", errors.Join(runErr, err))
	}
	terminal := controlPlaneTerminalResponse(outcome, runErr, repoDir)
	sanitizeHostAgentTerminal(&terminal, []string{repoDir, options.WorkRoot})
	completion := hostAgentCompletionRequest{
		Version: hostAgentAPIVersion, RunID: assignment.RunID, LockToken: assignment.LockToken,
		SessionID: options.SessionID, Terminal: terminal, Files: files,
	}
	var responseTerminal runCommandJSON
	completePath := "/v1/host-agents/" + options.AgentID + "/assignments/" + assignment.RunID + "/complete"
	if err := postHostAgentCompletion(options, completePath, completion, runtime, &responseTerminal); err != nil {
		return errors.Join(runErr, err)
	}
	_, _ = fmt.Fprintf(stdout, "agent: %s\nhost: %s\nrun: %s\nstatus: %s\n", options.AgentID, options.HostID, assignment.RunID, responseTerminal.Status)
	return runErr
}

func startHostAgentHeartbeat(options hostAgentRunOnceOptions, runtime runRuntime) (func(), <-chan error, <-chan error) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	heartbeatErrors := make(chan error, 1)
	executionCancel := make(chan error, 1)
	heartbeatRuntime := runtime
	heartbeatClient := controlPlaneHTTPClient(runtime.ControlPlaneHTTPClient)
	heartbeatClient.Timeout = hostAgentHeartbeatRequestTimeout
	heartbeatRuntime.ControlPlaneHTTPClient = heartbeatClient
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(hostAgentHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var status hostAgentStatusResponse
				err := postControlPlaneJSON(options.ControlPlane, options.CredentialEnv, "/v1/host-agents/"+options.AgentID+"/heartbeat", hostAgentNextRequest{
					Version: hostAgentAPIVersion, SessionID: options.SessionID,
				}, heartbeatRuntime, http.StatusOK, &status)
				if err != nil {
					heartbeatErr := fmt.Errorf("Windows Host Agent process-session heartbeat: %w", err)
					heartbeatErrors <- heartbeatErr
					executionCancel <- heartbeatErr
					return
				}
			case <-done:
				return
			}
		}
	}()
	var stoppedOnce bool
	return func() {
		if !stoppedOnce {
			stoppedOnce = true
			close(done)
			<-stopped
		}
	}, heartbeatErrors, executionCancel
}

func currentHostAgentHeartbeatError(heartbeatErrors <-chan error) error {
	select {
	case err := <-heartbeatErrors:
		return err
	default:
		return nil
	}
}

func postHostAgentCompletion(options hostAgentRunOnceOptions, path string, completion hostAgentCompletionRequest, runtime runRuntime, terminal *runCommandJSON) error {
	return postHostAgentMutation(options, path, completion, runtime, terminal)
}

func postHostAgentMutation(options hostAgentRunOnceOptions, path string, body any, runtime runRuntime, target any) error {
	for {
		err := postControlPlaneJSON(options.ControlPlane, options.CredentialEnv, path, body, runtime, http.StatusOK, target)
		if err == nil {
			return nil
		}
		var statusErr *controlPlaneHTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 {
			return err
		}
		time.Sleep(time.Second)
	}
}

func pollHostAgentAssignment(options hostAgentRunOnceOptions, runtime runRuntime, heartbeatErrors <-chan error) (hostAgentAssignmentResponse, error) {
	nextPath := "/v1/host-agents/" + options.AgentID + "/assignments/next"
	for {
		if err := currentHostAgentHeartbeatError(heartbeatErrors); err != nil {
			return hostAgentAssignmentResponse{}, err
		}
		var assignment hostAgentAssignmentResponse
		pollContext, cancelPoll := context.WithTimeout(context.Background(), 30*time.Second+hostAgentHeartbeatRequestTimeout)
		watcherDone := make(chan struct{})
		var watchedHeartbeatError error
		go func() {
			defer close(watcherDone)
			select {
			case heartbeatErr := <-heartbeatErrors:
				watchedHeartbeatError = heartbeatErr
				cancelPoll()
			case <-pollContext.Done():
			}
		}()
		err := postControlPlaneJSONWithLimitContext(pollContext, options.ControlPlane, options.CredentialEnv, nextPath, hostAgentNextRequest{Version: hostAgentAPIVersion, SessionID: options.SessionID}, runtime, http.StatusOK, &assignment, maximumControlPlaneSubmissionBytes+1024*1024)
		cancelPoll()
		<-watcherDone
		if watchedHeartbeatError != nil {
			return hostAgentAssignmentResponse{}, watchedHeartbeatError
		}
		if heartbeatErr := currentHostAgentHeartbeatError(heartbeatErrors); heartbeatErr != nil {
			return hostAgentAssignmentResponse{}, heartbeatErr
		}
		var statusErr *controlPlaneHTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNoContent {
			continue
		}
		if err != nil {
			return hostAgentAssignmentResponse{}, err
		}
		return assignment, nil
	}
}

func validateHostAgentAssignment(options hostAgentRunOnceOptions, assignment hostAgentAssignmentResponse) error {
	if assignment.Version != hostAgentAPIVersion || assignment.Kind != "host-agent-assignment" || assignment.AgentID != options.AgentID || assignment.HostID != options.HostID || validateRunID(assignment.RunID) != nil || assignment.LockToken == "" {
		return fmt.Errorf("Control Plane returned an unsupported Windows Host Agent assignment")
	}
	return nil
}

func failConfirmedHostAgentAssignment(options hostAgentRunOnceOptions, assignment hostAgentAssignmentResponse, runtime runRuntime, phase string, runErr error) error {
	failure := hostAgentFailureRequest{
		Version: hostAgentAPIVersion, RunID: assignment.RunID, LockToken: assignment.LockToken,
		SessionID: options.SessionID, Diagnostic: "Windows Host Agent failed while " + phase,
	}
	runRoot := filepath.Join(options.WorkRoot, "runs", assignment.RunID)
	if cleanupErr := os.RemoveAll(runRoot); cleanupErr != nil {
		return errors.Join(runErr, fmt.Errorf("clean failed Windows Host Agent workspace: %w", cleanupErr))
	}
	if _, cleanupErr := os.Lstat(runRoot); !errors.Is(cleanupErr, os.ErrNotExist) {
		return errors.Join(runErr, fmt.Errorf("verify failed Windows Host Agent workspace cleanup: %w", cleanupErr))
	}
	path := "/v1/host-agents/" + options.AgentID + "/assignments/" + assignment.RunID + "/fail"
	var terminal runCommandJSON
	failErr := postHostAgentMutation(options, path, failure, runtime, &terminal)
	return errors.Join(runErr, failErr)
}

func quarantineConfirmedHostAgentAssignment(options hostAgentRunOnceOptions, assignment hostAgentAssignmentResponse, runtime runRuntime, phase string, runErr error) error {
	failure := hostAgentFailureRequest{
		Version: hostAgentAPIVersion, RunID: assignment.RunID, LockToken: assignment.LockToken,
		SessionID: options.SessionID, Diagnostic: "Windows Host Agent could not verify Maya session shutdown while " + phase,
		Quarantine: true,
	}
	path := "/v1/host-agents/" + options.AgentID + "/assignments/" + assignment.RunID + "/fail"
	var terminal runCommandJSON
	quarantineErr := postHostAgentMutation(options, path, failure, runtime, &terminal)
	return errors.Join(runErr, quarantineErr, errors.New(failure.Diagnostic))
}

func writeHostAgentFakeHostConfig(options hostAgentRunOnceOptions, assignment hostAgentAssignmentResponse) (string, error) {
	targetProfile := assignment.Submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	profileName, err := json.Marshal(targetProfile)
	if err != nil {
		return "", err
	}
	hostID, err := json.Marshal(assignment.HostID)
	if err != nil {
		return "", err
	}
	runRoot := filepath.Join(options.WorkRoot, "runs", assignment.RunID)
	workRoot, err := json.Marshal(filepath.Join(runRoot, "host"))
	if err != nil {
		return "", err
	}
	content := []byte(fmt.Sprintf("version: 1\ntargetProfiles:\n  %s:\n    hostPool: agent\nhostPools:\n  agent:\n    hosts:\n      - id: %s\n        health: healthy\n        workRoot: %s\n", profileName, hostID, workRoot))
	path := filepath.Join(options.WorkRoot, "host-config.yaml")
	if err := writeRunLedgerBytes(path, content); err != nil {
		return "", err
	}
	return path, nil
}

func buildHostAgentResultFiles(repoDir string, runID string) ([]controlPlaneFile, error) {
	return buildHostAgentResultFilesSanitized(repoDir, runID, nil)
}

func buildHostAgentResultFilesSanitized(repoDir string, runID string, privateRoots []string) ([]controlPlaneFile, error) {
	var files []controlPlaneFile
	seen := make(map[string]bool)
	estimated := int64(4096)
	for _, root := range []string{
		filepath.ToSlash(filepath.Join(".maya-stall", "state", "ledger", "runs", runID)),
		filepath.ToSlash(filepath.Join("artifacts", "maya-stall", runID)),
	} {
		if err := appendHostAgentResultPath(repoDir, root, privateRoots, &files, seen, &estimated); err != nil {
			return nil, err
		}
	}
	if err := validateHostAgentResultFiles(runID, files); err != nil {
		return nil, err
	}
	return files, nil
}

func appendHostAgentResultPath(repoDir string, relativeRoot string, privateRoots []string, files *[]controlPlaneFile, seen map[string]bool, estimated *int64) error {
	root := filepath.Join(repoDir, filepath.FromSlash(relativeRoot))
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Windows Host Agent result must not contain symlinks")
		}
		relative, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if seen[relative] {
			return nil
		}
		seen[relative] = true
		if entry.IsDir() {
			if err := addControlPlaneSnapshotEstimate(estimated, 0, relative); err != nil {
				return err
			}
			*files = append(*files, controlPlaneFile{Path: relative, Kind: "directory"})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Windows Host Agent result must contain only regular files")
		}
		if err := addControlPlaneSnapshotEstimate(estimated, info.Size(), relative); err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if int64(len(content)) != info.Size() {
			return fmt.Errorf("Windows Host Agent result changed while reading")
		}
		if len(privateRoots) > 0 && utf8.Valid(content) && !bytes.ContainsRune(content, '\x00') {
			content = []byte(sanitizeHostAgentResultText(string(content), privateRoots))
		}
		*files = append(*files, controlPlaneFile{Path: relative, Kind: "file", Content: content})
		return nil
	})
}

func sanitizeHostAgentResultText(value string, privateRoots []string) string {
	variants := make([]string, 0, len(privateRoots)*3)
	for _, root := range privateRoots {
		if root == "" {
			continue
		}
		variants = append(variants, root, filepath.ToSlash(root), strings.ReplaceAll(root, `\`, `\\`))
	}
	sort.SliceStable(variants, func(left int, right int) bool { return len(variants[left]) > len(variants[right]) })
	for _, variant := range variants {
		value = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(variant)).ReplaceAllStringFunc(value, func(string) string {
			return "[agent-workspace]"
		})
	}
	return value
}

func sanitizeHostAgentTerminal(terminal *runCommandJSON, privateRoots []string) {
	terminal.Diagnostic = sanitizeHostAgentResultText(terminal.Diagnostic, privateRoots)
	terminal.RemediationHint = sanitizeHostAgentResultText(terminal.RemediationHint, privateRoots)
	terminal.Error = sanitizeHostAgentResultText(terminal.Error, privateRoots)
	for index := range terminal.FollowUpCommands {
		terminal.FollowUpCommands[index] = sanitizeHostAgentResultText(terminal.FollowUpCommands[index], privateRoots)
	}
}
