package cli

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const controlPlaneAPIVersion = 1
const defaultControlPlaneTokenEnv = "MAYA_STALL_CONTROL_PLANE_TOKEN"
const maximumControlPlaneSubmissionBytes = 32 * 1024 * 1024
const maximumControlPlaneSnapshotEstimateBytes = maximumControlPlaneSubmissionBytes - 64*1024
const maximumControlPlaneDefaultResponseBytes = 8 * 1024 * 1024
const maximumControlPlaneLedgerResponseBytes = 6*maximumRunLedgerMaxLogBytes + 1024*1024

type controlPlaneSubmission struct {
	Version         int                `json:"version"`
	Scenario        string             `json:"scenario"`
	TargetProfile   string             `json:"targetProfile,omitempty"`
	StopAfter       string             `json:"stopAfter"`
	ConfigName      string             `json:"configName"`
	Config          []byte             `json:"config"`
	Files           []controlPlaneFile `json:"files,omitempty"`
	SubmissionError string             `json:"submissionError,omitempty"`
}

type controlPlaneFile struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content []byte `json:"content,omitempty"`
}

type controlPlaneStatusResponse struct {
	Version       int    `json:"version"`
	Kind          string `json:"kind"`
	RunID         string `json:"runId"`
	Scenario      string `json:"scenario"`
	TargetProfile string `json:"targetProfile,omitempty"`
	Host          string `json:"host,omitempty"`
	State         string `json:"state"`
	Status        string `json:"status,omitempty"`
	CleanupState  string `json:"cleanupState"`
	AcceptedAt    string `json:"acceptedAt"`
	Evidence      string `json:"evidence"`
}

type controlPlaneEventsResponse struct {
	Version         int              `json:"version"`
	Kind            string           `json:"kind"`
	RunID           string           `json:"runId"`
	Events          []map[string]any `json:"events"`
	EventsOmitted   int              `json:"eventsOmitted"`
	EventsTruncated bool             `json:"eventsTruncated"`
}

type controlPlaneLogsResponse struct {
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	RunID     string `json:"runId"`
	Content   string `json:"content"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

type controlPlaneResultResponse struct {
	Version      int               `json:"version"`
	Kind         string            `json:"kind"`
	RunID        string            `json:"runId"`
	State        string            `json:"state"`
	Status       string            `json:"status"`
	CleanupState string            `json:"cleanupState"`
	Final        bool              `json:"final"`
	Success      bool              `json:"success"`
	Result       ScenarioResult    `json:"result"`
	Validators   []validatorResult `json:"validators,omitempty"`
	Evidence     string            `json:"evidence"`
}

type controlPlaneEvidenceResponse struct {
	Version int            `json:"version"`
	Kind    string         `json:"kind"`
	RunID   string         `json:"runId"`
	Bundle  evidenceBundle `json:"bundle"`
}

type runReadOptions struct {
	RunID                string
	JSON                 bool
	ControlPlane         string
	ControlPlaneTokenEnv string
}

type controlPlaneServeOptions struct {
	Listen   string
	DataDir  string
	TLSCert  string
	TLSKey   string
	TokenEnv string
}

func parseControlPlaneServeArgs(args []string, workDir string) (controlPlaneServeOptions, error) {
	options := controlPlaneServeOptions{Listen: "127.0.0.1:8443", TokenEnv: defaultControlPlaneTokenEnv}
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--listen", "--data-dir", "--tls-cert", "--tls-key", "--token-env":
			index++
			if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "--") {
				return controlPlaneServeOptions{}, newUsageError("%s needs a value", flag)
			}
			switch flag {
			case "--listen":
				options.Listen = args[index]
			case "--data-dir":
				options.DataDir = resolveFromRepo(workDir, args[index])
			case "--tls-cert":
				options.TLSCert = resolveFromRepo(workDir, args[index])
			case "--tls-key":
				options.TLSKey = resolveFromRepo(workDir, args[index])
			case "--token-env":
				options.TokenEnv = args[index]
			}
		default:
			return controlPlaneServeOptions{}, newUsageError("unknown control-plane serve option %q", flag)
		}
	}
	if options.DataDir == "" {
		return controlPlaneServeOptions{}, newUsageError("control-plane serve needs --data-dir")
	}
	if options.TLSCert == "" || options.TLSKey == "" {
		return controlPlaneServeOptions{}, newUsageError("control-plane serve needs --tls-cert and --tls-key")
	}
	_, port, err := net.SplitHostPort(options.Listen)
	if err != nil {
		return controlPlaneServeOptions{}, newUsageError("--listen needs host:port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return controlPlaneServeOptions{}, newUsageError("--listen needs a port between 1 and 65535")
	}
	return options, nil
}

func runControlPlaneServer(options controlPlaneServeOptions, runtime runRuntime, stdout io.Writer) error {
	token, ok := os.LookupEnv(options.TokenEnv)
	if !ok || token == "" {
		return fmt.Errorf("control plane token environment variable %s is not set", options.TokenEnv)
	}
	for _, path := range []string{options.TLSCert, options.TLSKey} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("TLS path %s must be a regular file, not a symlink", path)
		}
	}
	handler, err := newControlPlaneHandler(options.DataDir, token, runtime)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "controlPlane: https://%s\ndata: %s\n", options.Listen, options.DataDir)
	if runtime.ControlPlaneServe != nil {
		return runtime.ControlPlaneServe(options, handler)
	}
	server := &http.Server{
		Addr:              options.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       time.Minute,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	err = server.ListenAndServeTLS(options.TLSCert, options.TLSKey)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type controlPlaneHandler struct {
	dataDir string
	token   string
	runtime runRuntime
	mu      sync.Mutex
}

func newControlPlaneHandler(dataDir string, token string, runtime runRuntime) (http.Handler, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("control plane token must not be empty")
	}
	if dataDir == "" {
		return nil, fmt.Errorf("control plane data directory must not be empty")
	}
	var err error
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	if err := ensureControlPlaneRootDirectory(dataDir); err != nil {
		return nil, err
	}
	if err := ensurePrivateControlPlaneDirectory(filepath.Join(dataDir, "runs")); err != nil {
		return nil, err
	}
	if err := ensurePrivateControlPlaneDirectory(filepath.Join(dataDir, "fake-host")); err != nil {
		return nil, err
	}
	return &controlPlaneHandler{dataDir: dataDir, token: token, runtime: runtime}, nil
}

func ensureControlPlaneRootDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("control plane data path %s must be a directory, not a symlink", path)
	}
	if info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return fmt.Errorf("control plane data path %s must already be private (0700 or stricter)", path)
}

func ensurePrivateControlPlaneDirectory(path string) error {
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
		return fmt.Errorf("control plane data path %s must be a directory, not a symlink", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("control plane data path %s must already be private (0700 or stricter)", path)
	}
	return nil
}

func (handler *controlPlaneHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	provided, bearer := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !bearer || len(provided) != len(handler.token) || subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) != 1 {
		writeControlPlaneError(response, http.StatusUnauthorized, "authentication required")
		return
	}
	if request.URL.Path != "/v1/runs" || request.Method != http.MethodPost {
		handler.serveRunRead(response, request)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumControlPlaneSubmissionBytes+1))
	if err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "read submission")
		return
	}
	if len(body) > maximumControlPlaneSubmissionBytes {
		writeControlPlaneError(response, http.StatusRequestEntityTooLarge, "submission exceeds size limit")
		return
	}
	var submission controlPlaneSubmission
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submission); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid JSON submission")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid JSON submission")
		return
	}
	if submission.Version != controlPlaneAPIVersion || submission.Scenario == "" {
		writeControlPlaneError(response, http.StatusBadRequest, "unsupported submission")
		return
	}
	if !isValidStopAfter(submission.StopAfter) {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Stop Policy")
		return
	}
	if err := validateControlPlaneSubmissionFiles(submission); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid submission files")
		return
	}

	handler.mu.Lock()
	if request.Context().Err() != nil {
		handler.mu.Unlock()
		return
	}
	runID, repoDir, err := handler.reserveRun(submission.Scenario)
	if err != nil {
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusInternalServerError, "reserve run")
		return
	}
	if err := materializeControlPlaneSubmission(repoDir, submission); err != nil {
		_ = os.RemoveAll(filepath.Dir(repoDir))
		handler.mu.Unlock()
		writeControlPlaneError(response, http.StatusBadRequest, "invalid submission files")
		return
	}
	handler.mu.Unlock()
	if request.Context().Err() != nil {
		_ = os.RemoveAll(filepath.Dir(repoDir))
		return
	}
	targetProfile := submission.TargetProfile
	if targetProfile == "" {
		targetProfile = "default"
	}
	options := runOptions{
		ScenarioName: submission.Scenario, TargetProfile: targetProfile, StopAfter: submission.StopAfter,
		AssignedRunID: runID, SharedFakeWorkRoot: filepath.Join(handler.dataDir, "fake-host"),
	}
	serverRuntime := handler.runtime
	previousAccepted := serverRuntime.Accepted
	previousAcceptedCheck := serverRuntime.AcceptedCheck
	acceptanceStarted := false
	acceptedWritten := false
	var acceptanceErr error
	encoder := json.NewEncoder(response)
	serverRuntime.Accepted = func(acceptedOutcome runOutcome) {
		acceptanceStarted = true
		accepted := runCommandJSONForOutcome(acceptedOutcome)
		accepted.Kind = "run-accepted"
		accepted.Status = "submitted"
		accepted.StateDir = "/v1/runs/" + runID + "/status"
		accepted.EvidenceDir = "/v1/runs/" + runID + "/evidence"
		response.Header().Set("Content-Type", "application/x-ndjson")
		response.WriteHeader(http.StatusCreated)
		if err := encoder.Encode(accepted); err != nil {
			acceptanceErr = fmt.Errorf("write Control Plane acceptance: %w", err)
		} else if err := http.NewResponseController(response).Flush(); err != nil {
			acceptanceErr = fmt.Errorf("flush Control Plane acceptance: %w", err)
		} else {
			acceptedWritten = true
		}
		if previousAccepted != nil {
			previousAccepted(acceptedOutcome)
		}
	}
	serverRuntime.AcceptedCheck = func() error {
		if previousAcceptedCheck != nil {
			return errors.Join(acceptanceErr, previousAcceptedCheck())
		}
		return acceptanceErr
	}
	var outcome runOutcome
	var runErr error
	if submission.SubmissionError != "" {
		outcome, runErr = failAcceptedSubmission(repoDir, options, serverRuntime, errors.New(submission.SubmissionError))
	} else {
		outcome, runErr = runScenario(repoDir, options, serverRuntime)
	}
	if !acceptedWritten {
		if !acceptanceStarted {
			writeControlPlaneError(response, http.StatusInternalServerError, "accept run")
		}
		return
	}
	result := controlPlaneTerminalResponse(outcome, runErr, repoDir)
	_ = json.NewEncoder(response).Encode(result)
}

func controlPlaneTerminalResponse(outcome runOutcome, runErr error, repoDir string) runCommandJSON {
	result := runCommandJSONForOutcome(outcome)
	result.StateDir = "/v1/runs/" + outcome.RunID + "/status"
	result.EvidenceDir = "/v1/runs/" + outcome.RunID + "/evidence"
	result.Diagnostic = sanitizeControlPlaneText(result.Diagnostic, repoDir)
	runDiagnostic := ""
	if runErr != nil {
		runDiagnostic = sanitizeControlPlaneText(runErr.Error(), repoDir)
	}
	if runErr != nil && (result.Status == resultStatusPassed || result.Diagnostic == "" || runDiagnostic != result.Diagnostic) {
		result.Status = resultStatusFailed
		result.FailedLayer = string(failureLayerRunState)
		result.Diagnostic = runDiagnostic
		result.RemediationHint = earlyFailureRemediation(failureLayerRunState)
	}
	return result
}

func (handler *controlPlaneHandler) serveRunRead(response http.ResponseWriter, request *http.Request) {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if request.Method != http.MethodGet || len(parts) != 4 || parts[0] != "v1" || parts[1] != "runs" {
		http.NotFound(response, request)
		return
	}
	runID := parts[2]
	if err := validateRunID(runID); err != nil {
		writeControlPlaneError(response, http.StatusBadRequest, "invalid Run ID")
		return
	}
	repoDir := filepath.Join(handler.dataDir, "runs", runID, "repo")
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		var usageErr *usageError
		if errors.As(err, &usageErr) {
			writeControlPlaneError(response, http.StatusNotFound, "run not found")
			return
		}
		writeControlPlaneError(response, http.StatusInternalServerError, "read run status")
		return
	}
	switch parts[3] {
	case "status":
		result := controlPlaneStatusFromRecord(repoDir, record, "/v1/runs/"+runID+"/evidence")
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(result)
	case "events":
		result, err := readControlPlaneEvents(repoDir, record)
		if err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "read run events")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(result)
	case "logs":
		result, err := readControlPlaneLogs(repoDir, record)
		if err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "read run logs")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(result)
	case "result":
		result, err := readControlPlaneResult(repoDir, record)
		if err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "read run result")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(result)
	case "evidence":
		result, err := readControlPlaneEvidence(repoDir, record)
		if err != nil {
			writeControlPlaneError(response, http.StatusInternalServerError, "read run evidence")
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(result)
	default:
		http.NotFound(response, request)
	}
}

func readControlPlaneEvidence(repoDir string, record runLedgerRecord) (controlPlaneEvidenceResponse, error) {
	evidenceDir := filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir))
	if err := ensureWorkspacePathHasNoSymlinkAncestor(evidenceDir, evidenceBundleFileName); err != nil {
		return controlPlaneEvidenceResponse{}, err
	}
	content, err := os.ReadFile(filepath.Join(evidenceDir, evidenceBundleFileName))
	if err != nil {
		return controlPlaneEvidenceResponse{}, err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return controlPlaneEvidenceResponse{}, err
	}
	if bundle.RunID != record.RunID {
		return controlPlaneEvidenceResponse{}, fmt.Errorf("evidence bundle identifies another run")
	}
	if bundle.Failure != nil {
		bundle.Failure.Diagnostic = sanitizeControlPlaneText(bundle.Failure.Diagnostic, repoDir)
	}
	sanitizeControlPlaneValidators(bundle.Validators, repoDir)
	return controlPlaneEvidenceResponse{Version: controlPlaneAPIVersion, Kind: "evidence", RunID: record.RunID, Bundle: bundle}, nil
}

func controlPlaneStatusFromRecord(repoDir string, record runLedgerRecord, evidence string) controlPlaneStatusResponse {
	return controlPlaneStatusResponse{
		Version:       controlPlaneAPIVersion,
		Kind:          "status",
		RunID:         record.RunID,
		Scenario:      record.Scenario,
		TargetProfile: record.TargetProfile,
		Host:          record.Host,
		State:         record.State,
		Status:        record.Status,
		CleanupState:  controlPlaneCleanupState(repoDir, record),
		AcceptedAt:    record.AcceptedAt,
		Evidence:      evidence,
	}
}

func readControlPlaneResult(repoDir string, record runLedgerRecord) (controlPlaneResultResponse, error) {
	switch record.State {
	case "completed", "failed", "cleanup-failed", "kept":
	default:
		return controlPlaneResultResponse{
			Version: controlPlaneAPIVersion, Kind: "result", RunID: record.RunID,
			State: record.State, Status: record.Status, CleanupState: "pending",
			Final: false, Success: false, Result: ScenarioResult{Status: record.Status},
			Evidence: "/v1/runs/" + record.RunID + "/evidence",
		}, nil
	}
	evidenceDir := filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir))
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, record.EvidenceDir); err != nil {
		return controlPlaneResultResponse{}, err
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(evidenceDir, evidenceBundleFileName); err != nil {
		return controlPlaneResultResponse{}, err
	}
	content, err := os.ReadFile(filepath.Join(evidenceDir, evidenceBundleFileName))
	if err != nil {
		return controlPlaneResultResponse{}, err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return controlPlaneResultResponse{}, err
	}
	if bundle.RunID != record.RunID {
		return controlPlaneResultResponse{}, fmt.Errorf("evidence bundle identifies another run")
	}
	result := ScenarioResult{Status: bundle.Status}
	if bundle.ScenarioResult != "" {
		clean := cleanEvidenceArtifactPath(bundle.ScenarioResult)
		if clean == "" {
			return controlPlaneResultResponse{}, fmt.Errorf("invalid Scenario Result path")
		}
		if err := ensureWorkspacePathHasNoSymlinkAncestor(evidenceDir, clean); err != nil {
			return controlPlaneResultResponse{}, err
		}
		resultContent, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(clean)))
		if err != nil {
			return controlPlaneResultResponse{}, err
		}
		if err := json.Unmarshal(resultContent, &result); err != nil {
			return controlPlaneResultResponse{}, err
		}
	} else if bundle.Failure != nil {
		result.Summary = bundle.Failure.Diagnostic
	}
	result.Summary = sanitizeControlPlaneText(result.Summary, repoDir)
	sanitizeControlPlaneValidators(bundle.Validators, repoDir)
	cleanupState := controlPlaneCleanupState(repoDir, record)
	final := record.State == "completed" || record.State == "failed" || record.State == "cleanup-failed"
	success := record.State == "completed" && record.Status == resultStatusPassed && result.Status == resultStatusPassed && cleanupState == "completed"
	return controlPlaneResultResponse{
		Version: controlPlaneAPIVersion, Kind: "result", RunID: record.RunID,
		State: record.State, Status: record.Status, CleanupState: cleanupState,
		Final: final, Success: success, Result: result, Validators: bundle.Validators,
		Evidence: "/v1/runs/" + record.RunID + "/evidence",
	}, nil
}

func readControlPlaneLogs(repoDir string, record runLedgerRecord) (controlPlaneLogsResponse, error) {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(record.Log)); err != nil {
		return controlPlaneLogsResponse{}, err
	}
	content, err := readRunLedgerBytes(filepath.Join(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(record.Log)))
	if err != nil {
		return controlPlaneLogsResponse{}, err
	}
	sanitized := sanitizeControlPlaneText(string(content), repoDir)
	return controlPlaneLogsResponse{
		Version: controlPlaneAPIVersion, Kind: "logs", RunID: record.RunID,
		Content: sanitized, Bytes: len(sanitized), Truncated: record.LogTruncated,
	}, nil
}

func readControlPlaneEvents(repoDir string, record runLedgerRecord) (controlPlaneEventsResponse, error) {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(record.Events)); err != nil {
		return controlPlaneEventsResponse{}, err
	}
	content, err := readRunLedgerBytes(filepath.Join(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(record.Events)))
	if err != nil {
		return controlPlaneEventsResponse{}, err
	}
	events := make([]map[string]any, 0, record.EventCount)
	for index, line := range bytes.Split(bytes.TrimSpace(content), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			return controlPlaneEventsResponse{}, fmt.Errorf("parse event %d: %w", index+1, err)
		}
		event = sanitizeControlPlaneValue(event, repoDir).(map[string]any)
		events = append(events, event)
	}
	return controlPlaneEventsResponse{
		Version: controlPlaneAPIVersion, Kind: "events", RunID: record.RunID, Events: events,
		EventsOmitted: record.EventsOmitted, EventsTruncated: record.EventsTruncated,
	}, nil
}

func controlPlaneCleanupState(repoDir string, record runLedgerRecord) string {
	switch record.State {
	case "completed", "failed":
		content, err := os.ReadFile(filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir), evidenceBundleFileName))
		if err != nil {
			return "unresolved"
		}
		var bundle evidenceBundle
		if json.Unmarshal(content, &bundle) != nil || bundle.RunID != record.RunID {
			return "unresolved"
		}
		if bundle.Failure != nil && bundle.Failure.CleanupState != "" {
			return bundle.Failure.CleanupState
		}
		return "completed"
	case "kept":
		return "retained"
	case "cleanup-failed":
		return "failed"
	default:
		return "pending"
	}
}

func (handler *controlPlaneHandler) reserveRun(scenario string) (string, string, error) {
	base := handler.runtime.Now().UTC().Format("20060102T150405.000000000Z")
	for attempt := 0; ; attempt++ {
		runID := base
		if attempt > 0 {
			runID = fmt.Sprintf("%s-%d", base, attempt)
		}
		runRoot := filepath.Join(handler.dataDir, "runs", runID)
		if err := os.Mkdir(runRoot, 0o700); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return "", "", err
		}
		repoDir := filepath.Join(runRoot, "repo")
		if err := os.Mkdir(repoDir, 0o700); err != nil {
			return "", "", err
		}
		return runID, repoDir, nil
	}
}

func writeControlPlaneError(response http.ResponseWriter, status int, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{"version": controlPlaneAPIVersion, "error": message})
}

func runScenarioThroughMode(repoDir string, options runOptions, runtime runRuntime) (runOutcome, error) {
	if options.ControlPlane == "" {
		if options.ControlPlaneSet {
			return runOutcome{}, newUsageError("--control-plane needs an HTTPS URL")
		}
		if options.ControlPlaneTokenEnv != "" {
			return runOutcome{}, newUsageError("--control-plane-token-env requires --control-plane")
		}
		return runScenario(repoDir, options, runtime)
	}
	return submitControlPlaneScenario(repoDir, options, runtime)
}

func failAcceptedSubmissionThroughMode(repoDir string, options runOptions, runtime runRuntime, submissionErr error) (runOutcome, error) {
	if options.ControlPlane == "" {
		if options.ControlPlaneSet {
			return runOutcome{}, newUsageError("--control-plane needs an HTTPS URL")
		}
		if options.ControlPlaneTokenEnv != "" {
			return runOutcome{}, newUsageError("--control-plane-token-env requires --control-plane")
		}
		return failAcceptedSubmission(repoDir, options, runtime, submissionErr)
	}
	return submitControlPlaneScenarioWithFailure(repoDir, options, runtime, submissionErr)
}

func printStatusThroughMode(repoDir string, options statusOptions, stdout io.Writer, runtime runRuntime) error {
	if options.ControlPlane == "" {
		if options.ControlPlaneTokenEnv != "" {
			return newUsageError("--control-plane-token-env requires --control-plane")
		}
		if options.JSON {
			if options.RunID == "" {
				return newUsageError("embedded JSON status needs --run")
			}
			result, err := readEmbeddedStatusResponse(repoDir, options.RunID)
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(result)
		}
		return printStatus(repoDir, options, stdout)
	}
	if options.RunID == "" {
		return newUsageError("configured Control Plane status needs --run")
	}
	var result controlPlaneStatusResponse
	if err := getControlPlaneJSON(options.ControlPlane, options.ControlPlaneTokenEnv, "/v1/runs/"+options.RunID+"/status", runtime, &result); err != nil {
		return err
	}
	if result.Version != controlPlaneAPIVersion || result.Kind != "status" || result.RunID != options.RunID {
		return fmt.Errorf("control plane returned an unsupported status response")
	}
	if options.JSON {
		return json.NewEncoder(stdout).Encode(result)
	}
	_, err := fmt.Fprintf(stdout, "run: %s\nstate: %s\nscenario: %s\ntargetProfile: %s\nhost: %s\nstatus: %s\ncleanupState: %s\nacceptedAt: %s\nevidence: %s\n",
		result.RunID, result.State, result.Scenario, result.TargetProfile, result.Host, result.Status, result.CleanupState, result.AcceptedAt, result.Evidence)
	return err
}

func readEmbeddedStatusResponse(repoDir string, runID string) (controlPlaneStatusResponse, error) {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return controlPlaneStatusResponse{}, err
	}
	retained, err := runLedgerUsesRetainedState(repoDir, record)
	if err != nil {
		return controlPlaneStatusResponse{}, err
	}
	if !retained {
		return controlPlaneStatusFromRecord(repoDir, record, record.EvidenceDir), nil
	}
	run, err := readKeptRunState(repoDir, runID)
	if err != nil {
		return controlPlaneStatusResponse{}, err
	}
	if run.Record.Status == "running" && run.Record.StopPhase == "" {
		evidencePath := filepath.Join(repoDir, "artifacts", "maya-stall", runID, evidenceBundleFileName)
		if evidenceBytes, evidenceErr := os.ReadFile(evidencePath); evidenceErr == nil {
			if err := json.Unmarshal(evidenceBytes, &run.Bundle); err != nil {
				return controlPlaneStatusResponse{}, fmt.Errorf("parse run evidence: %w", err)
			}
		} else if !errors.Is(evidenceErr, os.ErrNotExist) {
			return controlPlaneStatusResponse{}, evidenceErr
		}
		run.Bundle.Status = run.Record.Status
	} else {
		run, err = readKeptRun(repoDir, runID)
		if err != nil {
			return controlPlaneStatusResponse{}, err
		}
	}
	if err := refreshKeptRunStatus(&run); err != nil {
		return controlPlaneStatusResponse{}, err
	}
	result := controlPlaneStatusFromRecord(repoDir, record, record.EvidenceDir)
	if run.RemoteStatus.State != "" {
		result.State = run.RemoteStatus.State
	}
	result.Status = run.Bundle.Status
	return result, nil
}

func parseRunReadArgs(command string, args []string) (runReadOptions, error) {
	var options runReadOptions
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--json":
			options.JSON = true
		case "--control-plane", "--control-plane-token-env":
			flag := args[index]
			index++
			if index >= len(args) || args[index] == "" || strings.HasPrefix(args[index], "--") {
				return runReadOptions{}, newUsageError("%s needs a value", flag)
			}
			if flag == "--control-plane" {
				options.ControlPlane = args[index]
			} else {
				options.ControlPlaneTokenEnv = args[index]
			}
		default:
			if strings.HasPrefix(args[index], "-") {
				return runReadOptions{}, newUsageError("unknown %s option %q", command, args[index])
			}
			if options.RunID != "" {
				return runReadOptions{}, newUsageError("%s needs one run id", command)
			}
			options.RunID = args[index]
		}
	}
	if err := validateRunID(options.RunID); err != nil {
		return runReadOptions{}, err
	}
	return options, nil
}

func printRunReadThroughMode(repoDir string, resource string, options runReadOptions, stdout io.Writer, runtime runRuntime) error {
	if options.ControlPlane == "" && options.ControlPlaneTokenEnv != "" {
		return newUsageError("--control-plane-token-env requires --control-plane")
	}
	switch resource {
	case "events":
		var result controlPlaneEventsResponse
		if options.ControlPlane != "" {
			if err := getControlPlaneJSON(options.ControlPlane, options.ControlPlaneTokenEnv, "/v1/runs/"+options.RunID+"/events", runtime, &result); err != nil {
				return err
			}
		} else {
			record, err := readRunLedgerRecord(repoDir, options.RunID)
			if err != nil {
				return err
			}
			result, err = readControlPlaneEvents(repoDir, record)
			if err != nil {
				return err
			}
		}
		if result.Version != controlPlaneAPIVersion || result.Kind != "events" || result.RunID != options.RunID {
			return fmt.Errorf("run event source returned an unsupported response")
		}
		if options.JSON {
			return json.NewEncoder(stdout).Encode(result)
		}
		if _, err := fmt.Fprintf(stdout, "run: %s\nevents:\n", result.RunID); err != nil {
			return err
		}
		for _, event := range result.Events {
			content, err := json.Marshal(event)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(stdout, string(content)); err != nil {
				return err
			}
		}
		if result.EventsTruncated {
			_, err := fmt.Fprintf(stdout, "eventsTruncated: true\neventsOmitted: %d\n", result.EventsOmitted)
			return err
		}
		return nil
	case "logs":
		var result controlPlaneLogsResponse
		if options.ControlPlane != "" {
			if err := getControlPlaneJSON(options.ControlPlane, options.ControlPlaneTokenEnv, "/v1/runs/"+options.RunID+"/logs", runtime, &result); err != nil {
				return err
			}
		} else {
			record, err := readRunLedgerRecord(repoDir, options.RunID)
			if err != nil {
				return err
			}
			result, err = readControlPlaneLogs(repoDir, record)
			if err != nil {
				return err
			}
		}
		if result.Version != controlPlaneAPIVersion || result.Kind != "logs" || result.RunID != options.RunID {
			return fmt.Errorf("run log source returned an unsupported response")
		}
		if options.JSON {
			return json.NewEncoder(stdout).Encode(result)
		}
		_, err := fmt.Fprintf(stdout, "run: %s\nlogs:\n%s", result.RunID, result.Content)
		if err == nil && result.Truncated {
			_, err = fmt.Fprintln(stdout, "logTruncated: true")
		}
		return err
	case "result":
		var result controlPlaneResultResponse
		if options.ControlPlane != "" {
			if err := getControlPlaneJSON(options.ControlPlane, options.ControlPlaneTokenEnv, "/v1/runs/"+options.RunID+"/result", runtime, &result); err != nil {
				return err
			}
		} else {
			record, err := readRunLedgerRecord(repoDir, options.RunID)
			if err != nil {
				return err
			}
			result, err = readControlPlaneResult(repoDir, record)
			if err != nil {
				return err
			}
		}
		if result.Version != controlPlaneAPIVersion || result.Kind != "result" || result.RunID != options.RunID {
			return fmt.Errorf("run result source returned an unsupported response")
		}
		if options.JSON {
			return json.NewEncoder(stdout).Encode(result)
		}
		_, err := fmt.Fprintf(stdout, "run: %s\nstate: %s\nstatus: %s\ncleanupState: %s\nfinal: %t\nsuccess: %t\nsummary: %s\nevidence: %s\n",
			result.RunID, result.State, result.Status, result.CleanupState, result.Final, result.Success, result.Result.Summary, result.Evidence)
		return err
	default:
		return fmt.Errorf("%s reads are not implemented", resource)
	}
}

func getControlPlaneJSON(rawURL string, tokenEnv string, path string, runtime runRuntime, target any) error {
	endpoint, err := parseControlPlaneURL(rawURL)
	if err != nil {
		return err
	}
	if tokenEnv == "" {
		tokenEnv = defaultControlPlaneTokenEnv
	}
	token, ok := os.LookupEnv(tokenEnv)
	if !ok || token == "" {
		return fmt.Errorf("control plane token environment variable %s is not set", tokenEnv)
	}
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(endpoint.String(), "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client := controlPlaneHTTPClient(runtime.ControlPlaneHTTPClient)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("read Control Plane: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("control plane read failed with HTTP %d", response.StatusCode)
	}
	limit := int64(maximumControlPlaneDefaultResponseBytes)
	if strings.HasSuffix(path, "/events") || strings.HasSuffix(path, "/logs") {
		// JSON can expand each retained byte to a six-byte Unicode escape.
		limit = int64(maximumControlPlaneLedgerResponseBytes)
	}
	return decodeBoundedControlPlaneJSON(response.Body, limit, target)
}

func parseControlPlaneURL(rawURL string) (*url.URL, error) {
	endpoint, err := url.Parse(rawURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" && endpoint.Path != "/" {
		return nil, newUsageError("--control-plane needs an origin-only HTTPS URL without credentials, path, query, or fragment")
	}
	return endpoint, nil
}

func controlPlaneHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func decodeBoundedControlPlaneJSON(reader io.Reader, limit int64, target any) error {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if int64(len(content)) > limit {
		return fmt.Errorf("control plane response exceeds %d bytes", limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("control plane response contains multiple JSON values")
		}
		return err
	}
	return nil
}

func validateControlPlaneSubmissionFiles(submission controlPlaneSubmission) error {
	if len(submission.SubmissionError) > 4096 {
		return fmt.Errorf("submission error exceeds size limit")
	}
	if submission.ConfigName == "" {
		if len(submission.Config) != 0 {
			return fmt.Errorf("repo run config content needs a configName")
		}
	} else if submission.ConfigName != defaultConfigName && submission.ConfigName != "maya-stall.yaml" {
		return fmt.Errorf("unsupported Repo Run Config name")
	}
	if len(submission.Files) > 10_000 {
		return fmt.Errorf("too many submission files")
	}
	kinds := make(map[string]string, len(submission.Files))
	for _, file := range submission.Files {
		clean, err := cleanRepoRelativePath(file.Path)
		if err != nil {
			return err
		}
		clean = filepath.ToSlash(clean)
		if clean != file.Path || clean == submission.ConfigName || kinds[clean] != "" {
			return fmt.Errorf("duplicate or non-canonical submission path")
		}
		if file.Kind != "file" && file.Kind != "directory" {
			return fmt.Errorf("unsupported submission file kind")
		}
		if file.Kind == "directory" && len(file.Content) != 0 {
			return fmt.Errorf("submission directory must not contain bytes")
		}
		kinds[clean] = file.Kind
	}
	for path := range kinds {
		for parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))); parent != "."; parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent))) {
			if kinds[parent] == "file" {
				return fmt.Errorf("submission file cannot contain child paths")
			}
		}
	}
	return nil
}

func sanitizeControlPlaneText(value string, repoDir string) string {
	if value == "" {
		return ""
	}
	privatePaths := []struct {
		path        string
		replacement string
	}{
		{path: repoDir, replacement: "<control-plane-run>"},
	}
	runRoot := filepath.Dir(repoDir)
	runsDir := filepath.Dir(runRoot)
	if filepath.Base(repoDir) == "repo" && filepath.Base(runsDir) == "runs" {
		privatePaths = append(privatePaths, struct {
			path        string
			replacement string
		}{path: filepath.Dir(runsDir), replacement: "<control-plane-data>"})
	}
	for _, private := range privatePaths {
		for _, privatePath := range []string{private.path, filepath.ToSlash(private.path)} {
			value = strings.ReplaceAll(value, privatePath, private.replacement)
		}
	}
	return value
}

func sanitizeControlPlaneValidators(validators []validatorResult, repoDir string) {
	for index := range validators {
		validators[index].Message = sanitizeControlPlaneText(validators[index].Message, repoDir)
	}
}

func sanitizeControlPlaneValue(value any, repoDir string) any {
	switch typed := value.(type) {
	case string:
		return sanitizeControlPlaneText(typed, repoDir)
	case []any:
		for index := range typed {
			typed[index] = sanitizeControlPlaneValue(typed[index], repoDir)
		}
		return typed
	case map[string]any:
		for key := range typed {
			typed[key] = sanitizeControlPlaneValue(typed[key], repoDir)
		}
		return typed
	default:
		return value
	}
}

func submitControlPlaneScenario(repoDir string, options runOptions, runtime runRuntime) (runOutcome, error) {
	return submitControlPlaneScenarioWithFailure(repoDir, options, runtime, nil)
}

func submitControlPlaneScenarioWithFailure(repoDir string, options runOptions, runtime runRuntime, submissionErr error) (runOutcome, error) {
	if options.HostOptionsSet {
		return runOutcome{}, newUsageError("configured Control Plane mode owns Maya Host selection and does not accept client host configuration")
	}
	endpoint, err := parseControlPlaneURL(options.ControlPlane)
	if err != nil {
		return runOutcome{}, err
	}
	submission, err := buildControlPlaneSubmission(repoDir, options)
	if err != nil {
		return runOutcome{}, err
	}
	if submissionErr != nil {
		submission.SubmissionError = submissionErr.Error()
	}
	content, err := json.Marshal(submission)
	if err != nil {
		return runOutcome{}, err
	}
	if len(content) > maximumControlPlaneSubmissionBytes {
		return runOutcome{}, fmt.Errorf("control plane submission exceeds %d bytes", maximumControlPlaneSubmissionBytes)
	}
	tokenEnv := options.ControlPlaneTokenEnv
	if tokenEnv == "" {
		tokenEnv = defaultControlPlaneTokenEnv
	}
	token, ok := os.LookupEnv(tokenEnv)
	if !ok || token == "" {
		return runOutcome{}, fmt.Errorf("control plane token environment variable %s is not set", tokenEnv)
	}
	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(endpoint.String(), "/")+"/v1/runs", bytes.NewReader(content))
	if err != nil {
		return runOutcome{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := controlPlaneHTTPClient(runtime.ControlPlaneHTTPClient)
	response, err := client.Do(request)
	if err != nil {
		return runOutcome{}, fmt.Errorf("submit Scenario to Control Plane: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusCreated {
		return runOutcome{}, fmt.Errorf("control plane submission failed with HTTP %d", response.StatusCode)
	}
	result, err := decodeControlPlaneSubmissionStream(response.Body, func(accepted runCommandJSON) {
		if runtime.Accepted != nil {
			runtime.Accepted(runOutcomeFromCommandJSON(accepted))
		}
	})
	if err != nil {
		return runOutcome{}, fmt.Errorf("decode Control Plane submission: %w", err)
	}
	outcome := runOutcomeFromCommandJSON(result)
	if outcome.Result.Status != resultStatusPassed {
		return outcome, fmt.Errorf("control plane run %s finished with status %s", outcome.RunID, outcome.Result.Status)
	}
	return outcome, nil
}

func decodeControlPlaneSubmissionStream(reader io.Reader, onAccepted func(runCommandJSON)) (runCommandJSON, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, 1024*1024+1))
	decoder.DisallowUnknownFields()
	var accepted runCommandJSON
	if err := decoder.Decode(&accepted); err != nil {
		return runCommandJSON{}, err
	}
	if accepted.Version != controlPlaneAPIVersion || accepted.Kind != "run-accepted" || !accepted.Accepted || accepted.RunID == "" {
		return runCommandJSON{}, fmt.Errorf("control plane returned an unsupported acceptance response")
	}
	if onAccepted != nil {
		onAccepted(accepted)
	}
	var terminal runCommandJSON
	if err := decoder.Decode(&terminal); err != nil {
		return runCommandJSON{}, err
	}
	if terminal.Version != controlPlaneAPIVersion || terminal.Kind != "run" || terminal.RunID != accepted.RunID {
		return runCommandJSON{}, fmt.Errorf("control plane returned an unsupported terminal response")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return runCommandJSON{}, fmt.Errorf("control plane submission contains more than two records")
		}
		return runCommandJSON{}, err
	}
	return terminal, nil
}

func buildControlPlaneSubmission(repoDir string, options runOptions) (controlPlaneSubmission, error) {
	submission := controlPlaneSubmission{
		Version: controlPlaneAPIVersion, Scenario: options.ScenarioName, TargetProfile: options.TargetProfile,
		StopAfter: options.StopAfter,
	}
	configPath, err := DiscoverConfig(repoDir)
	if errors.Is(err, errRepoRunConfigNotFound) {
		return submission, nil
	}
	if err != nil {
		return controlPlaneSubmission{}, err
	}
	configContent, configInfo, err := readControlPlaneSnapshotFile(repoDir, filepath.Base(configPath))
	if err != nil {
		return controlPlaneSubmission{}, err
	}
	estimatedBytes := int64(4096 + len(filepath.Base(configPath)))
	if err := addControlPlaneSnapshotEstimate(&estimatedBytes, configInfo.Size(), ""); err != nil {
		return controlPlaneSubmission{}, err
	}
	submission.ConfigName = filepath.Base(configPath)
	submission.Config = configContent
	var config repoRunConfig
	if err := decodeKnownYAMLFields(configContent, &config); err != nil || config.Version != 1 || len(config.Scenarios) == 0 {
		return submission, nil
	}
	scenario, err := resolveScenarioContract(config, options.ScenarioName)
	if err != nil {
		return submission, nil
	}
	seen := map[string]bool{submission.ConfigName: true}
	for _, payload := range scenario.Payload {
		if err := appendControlPlanePath(repoDir, payload.Source, &submission.Files, seen, &estimatedBytes); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return controlPlaneSubmission{}, err
		}
	}
	return submission, nil
}

func appendControlPlanePath(repoDir string, relativePath string, files *[]controlPlaneFile, seen map[string]bool, estimatedBytes *int64) error {
	clean, err := cleanRepoRelativePath(relativePath)
	if err != nil {
		return err
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(repoDir, clean); err != nil {
		return fmt.Errorf("payload path %s must stay within the repo without symlinks: %w", relativePath, err)
	}
	root := filepath.Join(repoDir, clean)
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("payload path %s must not contain a symlink", relativePath)
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
			if err := addControlPlaneSnapshotEstimate(estimatedBytes, 0, relative); err != nil {
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
			return fmt.Errorf("payload path %s must contain only regular files", relativePath)
		}
		if err := addControlPlaneSnapshotEstimate(estimatedBytes, info.Size(), relative); err != nil {
			return err
		}
		content, openedInfo, err := readControlPlaneSnapshotFile(repoDir, relative)
		if err != nil {
			return err
		}
		if openedInfo.Size() != info.Size() {
			return fmt.Errorf("payload path %s changed while creating the snapshot", relative)
		}
		*files = append(*files, controlPlaneFile{Path: relative, Kind: "file", Content: content})
		return nil
	})
}

func readControlPlaneSnapshotFile(repoDir string, relativePath string) ([]byte, os.FileInfo, error) {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(repoDir, relativePath); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(repoDir, filepath.FromSlash(relativePath))
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Size() < 0 || openedInfo.Size() > maximumControlPlaneSubmissionBytes {
		return nil, nil, fmt.Errorf("snapshot path %s must be a bounded regular file", relativePath)
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(repoDir, relativePath); err != nil {
		return nil, nil, err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return nil, nil, fmt.Errorf("snapshot path %s changed or became a symlink while opening", relativePath)
	}
	content, err := io.ReadAll(io.LimitReader(file, maximumControlPlaneSubmissionBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(content) > maximumControlPlaneSubmissionBytes || int64(len(content)) != openedInfo.Size() {
		return nil, nil, fmt.Errorf("snapshot path %s changed or exceeded the submission size limit while reading", relativePath)
	}
	return content, openedInfo, nil
}

func addControlPlaneSnapshotEstimate(total *int64, contentBytes int64, path string) error {
	if contentBytes < 0 || contentBytes > maximumControlPlaneSubmissionBytes {
		return fmt.Errorf("control plane snapshot exceeds submission size limit")
	}
	encodedBytes := ((contentBytes + 2) / 3) * 4
	encodedPath, err := json.Marshal(path)
	if err != nil {
		return err
	}
	*total += encodedBytes + int64(len(encodedPath)) + 128
	if *total > maximumControlPlaneSnapshotEstimateBytes {
		return fmt.Errorf("control plane snapshot exceeds submission size limit")
	}
	return nil
}

func materializeControlPlaneSubmission(repoDir string, submission controlPlaneSubmission) error {
	if submission.ConfigName == "" && len(submission.Config) == 0 {
		return nil
	}
	if submission.ConfigName != defaultConfigName && submission.ConfigName != "maya-stall.yaml" {
		return fmt.Errorf("unsupported Repo Run Config name")
	}
	if err := os.WriteFile(filepath.Join(repoDir, submission.ConfigName), submission.Config, 0o600); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, file := range submission.Files {
		clean, err := cleanRepoRelativePath(file.Path)
		if err != nil {
			return err
		}
		clean = filepath.ToSlash(clean)
		if seen[clean] || clean == submission.ConfigName {
			return fmt.Errorf("duplicate submission path")
		}
		seen[clean] = true
		path := filepath.Join(repoDir, filepath.FromSlash(clean))
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
			return fmt.Errorf("unsupported submission file kind")
		}
	}
	return nil
}

func runCommandJSONForOutcome(outcome runOutcome) runCommandJSON {
	result := runCommandJSON{
		Version: controlPlaneAPIVersion, Kind: "run", Accepted: outcome.Accepted, RunID: outcome.RunID,
		Scenario: outcome.Scenario, TargetProfile: outcome.TargetProfile, Host: outcome.Host,
		Status: outcome.Result.Status, StopPolicy: outcome.StopPolicy,
	}
	if outcome.Failure != nil {
		result.FailedLayer = outcome.Failure.FailedLayer
		result.Diagnostic = outcome.Failure.Diagnostic
		result.RemediationHint = outcome.Failure.RemediationHint
	}
	return result
}

func runOutcomeFromCommandJSON(result runCommandJSON) runOutcome {
	outcome := runOutcome{
		RunID: result.RunID, Scenario: result.Scenario, TargetProfile: result.TargetProfile, Host: result.Host,
		StateDir: result.StateDir, EvidenceDir: result.EvidenceDir, Result: ScenarioResult{Status: result.Status},
		StopPolicy: result.StopPolicy, FollowUpCommands: result.FollowUpCommands, Accepted: result.Accepted,
	}
	if result.FailedLayer != "" {
		outcome.Failure = &runFailureEvidence{FailedLayer: result.FailedLayer, Diagnostic: result.Diagnostic, RemediationHint: result.RemediationHint}
	}
	return outcome
}
