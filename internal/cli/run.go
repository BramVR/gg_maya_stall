package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const resultStatusPassed = "passed"
const resultStatusFailed = "failed"
const scenarioResultEnvVar = "MAYA_STALL_SCENARIO_RESULT"
const stopAfterSuccess = "success"
const stopAfterFailure = "failure"
const stopAfterAlways = "always"
const stopAfterNever = "never"

type runRuntime struct {
	Host   runHost
	Broker sessionBroker
	Now    func() time.Time
}

type runHost interface {
	StagePayload(runContext, []manifestPayload) error
}

type artifactCollector interface {
	CollectArtifacts(runContext, scenarioConfig) error
}

type sessionBroker interface {
	RunScenario(runContext, scenarioConfig) (ScenarioResult, error)
}

type screenshotCapturer interface {
	CaptureScreenshot(runContext, screenshotRequest) (visualEvidenceArtifact, error)
}

type recordingCapturer interface {
	CaptureRecording(runContext, recordingRequest) (visualEvidenceArtifact, error)
}

type screenshotRequest struct {
	Name string
}

type recordingRequest struct {
	Name     string
	Duration time.Duration
	FPS      int
}

type runContext struct {
	RepoDir            string
	StateDir           string
	EvidenceDir        string
	Workspace          string
	EventsPath         string
	LogPath            string
	ScenarioResultPath string
	Environment        map[string]string
}

type runOutcome struct {
	RunID            string
	Scenario         string
	TargetProfile    string
	Host             string
	StateDir         string
	EvidenceDir      string
	Result           ScenarioResult
	Validators       []validatorResult
	StopPolicy       string
	FollowUpCommands []string
}

type runManifest struct {
	RunID         string            `json:"runId"`
	Scenario      string            `json:"scenario"`
	TargetProfile string            `json:"targetProfile"`
	Host          string            `json:"host"`
	ConfigPath    string            `json:"configPath"`
	Payload       []manifestPayload `json:"payload"`
}

type manifestPayload struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Staged string `json:"staged"`
}

type ScenarioResult struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
}

type scenarioResultDocument struct {
	Result ScenarioResult
	Fields map[string]any
}

type evidenceBundle struct {
	RunID          string                   `json:"runId"`
	Scenario       string                   `json:"scenario"`
	Status         string                   `json:"status"`
	TargetProfile  string                   `json:"targetProfile"`
	Host           string                   `json:"host"`
	Manifest       string                   `json:"manifest"`
	Events         string                   `json:"events"`
	Log            string                   `json:"log"`
	ScenarioResult string                   `json:"scenarioResult"`
	Payload        []manifestPayload        `json:"payload"`
	VisualEvidence []visualEvidenceArtifact `json:"visualEvidence,omitempty"`
	Outputs        []outputArtifact         `json:"outputs,omitempty"`
	Validators     []validatorResult        `json:"validators,omitempty"`
}

type visualEvidenceArtifact struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
}

type outputArtifact struct {
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
}

type validatorResult struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func runScenario(repoDir string, options runOptions, runtime runRuntime) (outcome runOutcome, err error) {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}

	config, configPath, err := loadRepoRunConfig(repoDir)
	if err != nil {
		return runOutcome{}, err
	}
	scenario, ok := config.Scenarios[options.ScenarioName]
	if !ok {
		return runOutcome{}, newUsageError("unknown Scenario %q", options.ScenarioName)
	}
	if scenario.ExpectedOutputs.ScenarioResult == "" {
		return runOutcome{}, fmt.Errorf("Scenario %q missing expectedOutputs.scenarioResult", options.ScenarioName)
	}
	scenarioResultPath, err := cleanRepoRelativePath(scenario.ExpectedOutputs.ScenarioResult)
	if err != nil {
		return runOutcome{}, err
	}
	host, err := selectHostForRun(repoDir, options)
	if err != nil {
		return runOutcome{}, err
	}
	if runtime.Host == nil {
		runtime.Host = runHostForConfig(host.Config)
	}
	if runtime.Broker == nil {
		runtime.Broker = sessionBrokerForConfig(host.Config)
	}
	releaseHostLock := true
	defer func() {
		if releaseHostLock {
			if releaseErr := host.release(); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release Host Lock for %s: %w", host.HostID, releaseErr))
			}
		}
	}()
	if err := rejectUnsupportedEvidenceConfig(runtime.Broker, scenario); err != nil {
		return runOutcome{}, err
	}
	defer func() {
		if err == nil && outcome.StopPolicy == "stopped" {
			if cleanupErr := cleanupRunState(repoDir, outcome.RunID); cleanupErr != nil {
				err = fmt.Errorf("clean up Fresh Run state for %s: %w", outcome.RunID, cleanupErr)
			}
		}
	}()

	runID := runtime.Now().UTC().Format("20060102T150405.000000000Z")
	if host.release == nil {
		host.release = func() error { return nil }
	}

	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	evidenceDir := filepath.Join(repoDir, "artifacts", "maya-stall", runID)
	workspace := filepath.Join(stateDir, "workspace")
	workspaceScenarioResultPath := filepath.Join(workspace, scenarioResultPath)
	context := runContext{
		RepoDir:            repoDir,
		StateDir:           stateDir,
		EvidenceDir:        evidenceDir,
		Workspace:          workspace,
		EventsPath:         filepath.Join(stateDir, "events.jsonl"),
		LogPath:            filepath.Join(stateDir, "logs", "session.log"),
		ScenarioResultPath: workspaceScenarioResultPath,
		Environment: map[string]string{
			scenarioResultEnvVar: workspaceScenarioResultPath,
		},
	}
	if err := createCleanRunDirs(context); err != nil {
		return runOutcome{}, err
	}

	payload, err := buildManifestPayload(scenario.Payload)
	if err != nil {
		return runOutcome{}, err
	}
	if err := runtime.Host.StagePayload(context, payload); err != nil {
		return runOutcome{}, err
	}

	manifest := runManifest{
		RunID:         runID,
		Scenario:      options.ScenarioName,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		ConfigPath:    repoRelativePath(repoDir, configPath),
		Payload:       payload,
	}
	if err := writeJSONFile(filepath.Join(stateDir, "manifest.json"), manifest); err != nil {
		return runOutcome{}, err
	}
	if err := appendEvent(context.EventsPath, "run.started", options.ScenarioName); err != nil {
		return runOutcome{}, err
	}

	result, err := runtime.Broker.RunScenario(context, scenario)
	if err != nil {
		return runOutcome{}, err
	}
	if collector, ok := runtime.Host.(artifactCollector); ok {
		if err := collector.CollectArtifacts(context, scenario); err != nil {
			return runOutcome{}, err
		}
	}
	brokerResult := result
	if err := validateScenarioResultPath(context, scenarioResultPath); err != nil {
		return runOutcome{}, err
	}
	resultDocument, found, err := readScenarioResultDocument(context.ScenarioResultPath)
	if err != nil {
		return runOutcome{}, err
	}
	if found {
		result = resultDocument.Result
		if result.Status == "" {
			result.Status = brokerResult.Status
		}
		if result.Summary == "" {
			result.Summary = brokerResult.Summary
		}
	} else {
		resultDocument = newScenarioResultDocument(result)
	}
	if result.Status == "" {
		result.Status = resultStatusPassed
	}
	visualEvidence, err := collectScenarioVisualEvidence(runtime.Broker, context, options.ScenarioName, scenario.Evidence)
	if err != nil {
		return runOutcome{}, err
	}
	resultDocument.setResult(result)
	if err := writeScenarioResult(context, scenarioResultPath, resultDocument); err != nil {
		return runOutcome{}, err
	}
	validatorResults, err := validateRunOutputs(context, scenario, result)
	if err != nil {
		return runOutcome{}, err
	}
	if hasValidatorFailure(validatorResults) {
		result.Status = resultStatusFailed
	}
	resultDocument.setResult(result)
	if err := writeScenarioResult(context, scenarioResultPath, resultDocument); err != nil {
		return runOutcome{}, err
	}
	stopPolicy := "stopped"
	followUpCommands := []string(nil)
	if !shouldStopAfter(options.StopAfter, result.Status) {
		stopPolicy = "kept"
		followUpCommands = append(followUpCommands,
			fmt.Sprintf("maya-stall status --run %s", runID),
			fmt.Sprintf("maya-stall attach %s", runID),
			fmt.Sprintf("maya-stall stop %s", runID),
		)
	}
	if err := appendEvent(context.EventsPath, "run.completed", result.Status); err != nil {
		return runOutcome{}, err
	}
	if err := writeEvidenceBundle(context, manifest, scenario, result, visualEvidence, validatorResults); err != nil {
		return runOutcome{}, err
	}
	if stopPolicy == "kept" {
		if err := markHostLockKept(repoDir, host.HostID, runID); err != nil {
			return runOutcome{}, err
		}
		releaseHostLock = false
	}

	return runOutcome{
		RunID:            runID,
		Scenario:         options.ScenarioName,
		TargetProfile:    host.TargetProfile,
		Host:             host.HostID,
		StateDir:         stateDir,
		EvidenceDir:      evidenceDir,
		Result:           result,
		Validators:       validatorResults,
		StopPolicy:       stopPolicy,
		FollowUpCommands: followUpCommands,
	}, nil
}

func rejectUnsupportedEvidenceConfig(broker sessionBroker, scenario scenarioConfig) error {
	if scenario.Evidence.Recording.Enabled {
		if _, ok := broker.(ggMayaSessiondBroker); ok {
			return fmt.Errorf("gg_mayasessiond does not expose recording capture; disable recording evidence or use screenshot/viewport capture")
		}
	}
	return nil
}

func collectScenarioVisualEvidence(broker sessionBroker, context runContext, scenarioName string, config evidenceConfig) ([]visualEvidenceArtifact, error) {
	var artifacts []visualEvidenceArtifact
	if config.Screenshots.Enabled {
		capturer, ok := broker.(screenshotCapturer)
		if !ok {
			return nil, fmt.Errorf("Session Broker does not support screenshot capture")
		}
		artifact, err := capturer.CaptureScreenshot(context, screenshotRequest{Name: visualEvidenceFileName(scenarioName, ".png")})
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	if config.Recording.Enabled {
		capturer, ok := broker.(recordingCapturer)
		if !ok {
			return nil, fmt.Errorf("Session Broker does not support recording capture")
		}
		artifact, err := capturer.CaptureRecording(context, recordingRequest{Name: visualEvidenceFileName(scenarioName, ".mp4"), Duration: defaultRecordingDuration, FPS: defaultRecordingFPS})
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func visualEvidenceFileName(name string, extension string) string {
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.' || r == '_' || r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	clean := strings.Trim(builder.String(), ".-")
	if clean == "" {
		clean = "scenario"
	}
	return clean + extension
}

func isValidStopAfter(value string) bool {
	switch value {
	case stopAfterSuccess, stopAfterFailure, stopAfterAlways, stopAfterNever:
		return true
	default:
		return false
	}
}

func shouldStopAfter(stopAfter string, status string) bool {
	switch stopAfter {
	case stopAfterSuccess:
		return status == resultStatusPassed
	case stopAfterFailure:
		return status != resultStatusPassed
	case stopAfterNever:
		return false
	case stopAfterAlways, "":
		return true
	default:
		return true
	}
}

func createCleanRunDirs(context runContext) error {
	for _, path := range []string{
		filepath.Join(".maya-stall", "state", "runs"),
		filepath.Join("artifacts", "maya-stall"),
	} {
		if err := ensureOutputPathHasNoSymlinkParent(context.RepoDir, path); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(context.StateDir), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(context.StateDir, 0o755); err != nil {
		return fmt.Errorf("create clean run state: %w", err)
	}
	for _, path := range []string{
		context.Workspace,
		filepath.Join(context.StateDir, "payload"),
		filepath.Dir(context.LogPath),
		context.EvidenceDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ensureOutputPathHasNoSymlinkParent(repoDir string, relativePath string) error {
	current := repoDir
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output path %s must not be a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("output path %s must be a directory", current)
		}
	}
	return nil
}

func buildManifestPayload(payload runPayload) ([]manifestPayload, error) {
	var manifest []manifestPayload
	for _, item := range []struct {
		kind  string
		paths []string
	}{
		{kind: "mayaScripts", paths: append(payload.MayaScripts, payload.Scripts...)},
		{kind: "scenes", paths: payload.Scenes},
		{kind: "pluginArtifacts", paths: payload.PluginArtifacts},
		{kind: "expectedOutputs", paths: payload.ExpectedOutputs},
		{kind: "includePaths", paths: payload.IncludePaths},
	} {
		for _, source := range item.paths {
			cleanSource, err := cleanRepoRelativePath(source)
			if err != nil {
				return nil, err
			}
			cleanSource = filepath.ToSlash(cleanSource)
			manifest = append(manifest, manifestPayload{
				Kind:   item.kind,
				Source: cleanSource,
				Staged: filepath.Join("payload", item.kind, cleanSource),
			})
		}
	}
	return manifest, nil
}

func cleanRepoRelativePath(path string) (string, error) {
	clean := filepath.Clean(path)
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("repo path %q must be repo-relative", path)
	}
	if isReservedPayloadPath(clean) {
		return "", fmt.Errorf("repo path %q is reserved for Maya Stall run state and artifacts", path)
	}
	return clean, nil
}

func isReservedPayloadPath(path string) bool {
	slashed := strings.ToLower(filepath.ToSlash(path))
	for _, reserved := range []string{".maya-stall", "artifacts/maya-stall"} {
		if slashed == reserved ||
			strings.HasPrefix(slashed, reserved+"/") ||
			strings.HasPrefix(reserved, slashed+"/") {
			return true
		}
	}
	return false
}

func newScenarioResultDocument(result ScenarioResult) scenarioResultDocument {
	document := scenarioResultDocument{Fields: make(map[string]any)}
	document.setResult(result)
	return document
}

func readScenarioResultDocument(path string) (scenarioResultDocument, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return scenarioResultDocument{}, false, nil
	}
	if err != nil {
		return scenarioResultDocument{}, false, err
	}
	var fields map[string]any
	if err := decodeJSONUseNumber(content, &fields); err != nil {
		return scenarioResultDocument{}, false, fmt.Errorf("parse Scenario Result %s: %w", path, err)
	}
	if fields == nil {
		return scenarioResultDocument{}, false, fmt.Errorf("Scenario Result %s must be a JSON object", path)
	}
	var result ScenarioResult
	if err := decodeJSONUseNumber(content, &result); err != nil {
		return scenarioResultDocument{}, false, fmt.Errorf("parse Scenario Result %s: %w", path, err)
	}
	return scenarioResultDocument{Result: result, Fields: fields}, true, nil
}

func (document *scenarioResultDocument) setResult(result ScenarioResult) {
	document.Result = result
	document.Fields["status"] = result.Status
	if result.Summary != "" {
		document.Fields["summary"] = result.Summary
	} else {
		delete(document.Fields, "summary")
	}
}

func writeScenarioResult(context runContext, resultPath string, result scenarioResultDocument) error {
	if err := validateScenarioResultPath(context, resultPath); err != nil {
		return err
	}
	workspaceResult := filepath.Join(context.Workspace, resultPath)
	if err := writeJSONFile(workspaceResult, result.Fields); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(context.EvidenceDir, "scenario-result.json"), result.Fields)
}

func validateScenarioResultPath(context runContext, resultPath string) error {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(context.Workspace, resultPath); err != nil {
		return fmt.Errorf("Scenario Result path %q: %w", resultPath, err)
	}
	return nil
}

func validateRunOutputs(context runContext, scenario scenarioConfig, result ScenarioResult) ([]validatorResult, error) {
	var results []validatorResult
	for _, validator := range scenario.Validators {
		switch validator.Type {
		case "scenarioResultStatus":
			want := validator.Status
			if want == "" {
				want = resultStatusPassed
			}
			if result.Status == want {
				results = append(results, validatorResult{Type: validator.Type, Status: resultStatusPassed, Message: fmt.Sprintf("Scenario Result status is %q", result.Status)})
			} else {
				results = append(results, validatorResult{Type: validator.Type, Status: resultStatusFailed, Message: fmt.Sprintf("Scenario Result status %q, want %q", result.Status, want)})
			}
		case "outputExists":
			results = append(results, validateOutputExists(context, validator))
		case "jsonEquals":
			results = append(results, validateJSONEquals(context, validator))
		case "numericApprox":
			results = append(results, validateNumericApprox(context, validator))
		case "fileHash":
			results = append(results, validateFileHash(context, validator))
		case "visualEvidence":
			results = append(results, validateVisualEvidence(context, validator))
		default:
			return nil, fmt.Errorf("unknown Validator type %q", validator.Type)
		}
	}
	return results, nil
}

func validateOutputExists(context runContext, validator validatorConfig) validatorResult {
	path, err := validatorWorkspacePath(context, validator)
	if err != nil {
		return failedValidator(validator.Type, err.Error())
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return failedValidator(validator.Type, fmt.Sprintf("required output %q is missing", validator.Path))
	}
	if err != nil {
		return failedValidator(validator.Type, err.Error())
	}
	if info.IsDir() {
		return failedValidator(validator.Type, fmt.Sprintf("required output %q is a directory", validator.Path))
	}
	return passedValidator(validator.Type, fmt.Sprintf("required output %q exists", validator.Path))
}

func validateJSONEquals(context runContext, validator validatorConfig) validatorResult {
	value, result := validatorJSONValue(context, validator)
	if result.Status == resultStatusFailed {
		return result
	}
	if valuesEqual(value, validator.Equals) {
		return passedValidator(validator.Type, fmt.Sprintf("%s equals expected value", validator.JSONPath))
	}
	return failedValidator(validator.Type, fmt.Sprintf("%s = %v, want %v", validator.JSONPath, value, validator.Equals))
}

func validateNumericApprox(context runContext, validator validatorConfig) validatorResult {
	value, result := validatorJSONValue(context, validator)
	if result.Status == resultStatusFailed {
		return result
	}
	if got, want, ok := numericPair(value, validator.Equals); ok {
		if math.Abs(got-want) <= validator.Tolerance {
			return passedValidator(validator.Type, fmt.Sprintf("%s = %v within %v of %v", validator.JSONPath, got, validator.Tolerance, want))
		}
		return failedValidator(validator.Type, fmt.Sprintf("%s = %v, want %v +/- %v", validator.JSONPath, got, want, validator.Tolerance))
	}
	gotArray, gotOK := numericArray(value)
	wantArray, wantOK := numericArray(validator.Equals)
	if !gotOK || !wantOK {
		return failedValidator(validator.Type, "numericApprox values must be numeric scalars or arrays")
	}
	if len(gotArray) != len(wantArray) {
		return failedValidator(validator.Type, fmt.Sprintf("%s length = %d, want %d", validator.JSONPath, len(gotArray), len(wantArray)))
	}
	for index := range gotArray {
		if math.Abs(gotArray[index]-wantArray[index]) > validator.Tolerance {
			return failedValidator(validator.Type, fmt.Sprintf("%s[%d] = %v, want %v +/- %v", validator.JSONPath, index, gotArray[index], wantArray[index], validator.Tolerance))
		}
	}
	return passedValidator(validator.Type, fmt.Sprintf("%s numeric array within %v", validator.JSONPath, validator.Tolerance))
}

func validateFileHash(context runContext, validator validatorConfig) validatorResult {
	path, err := validatorWorkspacePath(context, validator)
	if err != nil {
		return failedValidator(validator.Type, err.Error())
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return failedValidator(validator.Type, fmt.Sprintf("hashed file %q is missing", validator.Path))
	}
	if err != nil {
		return failedValidator(validator.Type, err.Error())
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	if strings.EqualFold(hash, validator.SHA256) {
		return passedValidator(validator.Type, fmt.Sprintf("%q sha256 matches", validator.Path))
	}
	return failedValidator(validator.Type, fmt.Sprintf("%q sha256 %s, want %s", validator.Path, hash, validator.SHA256))
}

func validateVisualEvidence(context runContext, validator validatorConfig) validatorResult {
	required := true
	if validator.Required != nil {
		required = *validator.Required
	}
	if !required {
		return passedValidator(validator.Type, "Visual Evidence not required")
	}
	for _, dir := range []string{"screenshots", "recordings", "visual"} {
		found, err := hasRegularFile(filepath.Join(context.EvidenceDir, dir))
		if err != nil {
			return failedValidator(validator.Type, err.Error())
		}
		if found {
			return passedValidator(validator.Type, "Visual Evidence present")
		}
	}
	return failedValidator(validator.Type, "Visual Evidence is missing")
}

func validatorWorkspacePath(context runContext, validator validatorConfig) (string, error) {
	if validator.Path == "" {
		return "", fmt.Errorf("%s Validator missing path", validator.Type)
	}
	path, err := cleanRepoRelativePath(validator.Path)
	if err != nil {
		return "", err
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(context.Workspace, path); err != nil {
		return "", err
	}
	return filepath.Join(context.Workspace, path), nil
}

func ensureWorkspacePathHasNoSymlinkAncestor(workspace string, relativePath string) error {
	current := workspace
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Validator output path %s must not be or contain a symlink", current)
		}
	}
	return nil
}

func validatorJSONValue(context runContext, validator validatorConfig) (any, validatorResult) {
	path, err := validatorWorkspacePath(context, validator)
	if err != nil {
		return nil, failedValidator(validator.Type, err.Error())
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, failedValidator(validator.Type, fmt.Sprintf("JSON file %q is missing", validator.Path))
	}
	if err != nil {
		return nil, failedValidator(validator.Type, err.Error())
	}
	var document any
	if err := decodeJSONUseNumber(content, &document); err != nil {
		return nil, failedValidator(validator.Type, fmt.Sprintf("parse JSON %q: %v", validator.Path, err))
	}
	value, ok := lookupJSONPath(document, validator.JSONPath)
	if !ok {
		return nil, failedValidator(validator.Type, fmt.Sprintf("JSON path %q is missing", validator.JSONPath))
	}
	return value, passedValidator(validator.Type, "")
}

func lookupJSONPath(document any, path string) (any, bool) {
	if path == "$" {
		return document, true
	}
	if !strings.HasPrefix(path, "$.") {
		return nil, false
	}
	current := document
	for _, part := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func valuesEqual(got any, want any) bool {
	gotNumber, gotNumeric := numberValue(got)
	wantNumber, wantNumeric := numberValue(want)
	if gotNumeric && wantNumeric {
		return gotNumber == wantNumber
	}
	gotSlice, gotIsSlice := got.([]any)
	wantSlice, wantIsSlice := want.([]any)
	if gotIsSlice || wantIsSlice {
		if !gotIsSlice || !wantIsSlice || len(gotSlice) != len(wantSlice) {
			return false
		}
		for index := range gotSlice {
			if !valuesEqual(gotSlice[index], wantSlice[index]) {
				return false
			}
		}
		return true
	}
	gotMap, gotIsMap := got.(map[string]any)
	wantMap, wantIsMap := want.(map[string]any)
	if gotIsMap || wantIsMap {
		if !gotIsMap || !wantIsMap || len(gotMap) != len(wantMap) {
			return false
		}
		for key, gotValue := range gotMap {
			wantValue, ok := wantMap[key]
			if !ok || !valuesEqual(gotValue, wantValue) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case json.Number:
		parsed, err := number.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numericPair(got any, want any) (float64, float64, bool) {
	gotNumber, gotOK := numberValue(got)
	wantNumber, wantOK := numberValue(want)
	return gotNumber, wantNumber, gotOK && wantOK
}

func numericArray(value any) ([]float64, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	numbers := make([]float64, 0, len(items))
	for _, item := range items {
		number, ok := numberValue(item)
		if !ok {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

func hasRegularFile(dir string) (bool, error) {
	errFound := errors.New("found regular file")
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				return errFound
			}
		}
		return nil
	})
	if errors.Is(err, errFound) {
		return true, nil
	}
	return false, err
}

func passedValidator(validatorType string, message string) validatorResult {
	return validatorResult{Type: validatorType, Status: resultStatusPassed, Message: message}
}

func failedValidator(validatorType string, message string) validatorResult {
	return validatorResult{Type: validatorType, Status: resultStatusFailed, Message: message}
}

func hasValidatorFailure(results []validatorResult) bool {
	for _, result := range results {
		if result.Status != resultStatusPassed {
			return true
		}
	}
	return false
}

func writeEvidenceBundle(context runContext, manifest runManifest, scenario scenarioConfig, result ScenarioResult, capturedVisualEvidence []visualEvidenceArtifact, validators []validatorResult) error {
	outputs, err := copyEvidenceOutputs(context, scenario)
	if err != nil {
		return err
	}
	if err := copyFile(filepath.Join(context.StateDir, "manifest.json"), filepath.Join(context.EvidenceDir, "manifest.json")); err != nil {
		return err
	}
	if err := copyFile(context.EventsPath, filepath.Join(context.EvidenceDir, "events.jsonl")); err != nil {
		return err
	}
	if err := copyFile(context.LogPath, filepath.Join(context.EvidenceDir, "logs", "session.log")); err != nil {
		return err
	}
	visualEvidence, err := discoverVisualEvidence(context.EvidenceDir)
	if err != nil {
		return err
	}
	visualEvidence = mergeVisualEvidence(capturedVisualEvidence, visualEvidence)
	bundle := evidenceBundle{
		RunID:          manifest.RunID,
		Scenario:       manifest.Scenario,
		Status:         result.Status,
		TargetProfile:  manifest.TargetProfile,
		Host:           manifest.Host,
		Manifest:       "manifest.json",
		Events:         "events.jsonl",
		Log:            filepath.Join("logs", "session.log"),
		ScenarioResult: "scenario-result.json",
		Payload:        manifest.Payload,
		VisualEvidence: visualEvidence,
		Outputs:        outputs,
		Validators:     validators,
	}
	return writeJSONFile(filepath.Join(context.EvidenceDir, "evidence.json"), bundle)
}

func mergeVisualEvidence(preferred []visualEvidenceArtifact, discovered []visualEvidenceArtifact) []visualEvidenceArtifact {
	seen := make(map[string]bool)
	artifacts := make([]visualEvidenceArtifact, 0, len(preferred)+len(discovered))
	add := func(artifact visualEvidenceArtifact) {
		artifact.Path = filepath.ToSlash(artifact.Path)
		if artifact.Path == "" || seen[artifact.Path] {
			return
		}
		seen[artifact.Path] = true
		artifacts = append(artifacts, artifact)
	}
	for _, artifact := range preferred {
		add(artifact)
	}
	for _, artifact := range discovered {
		add(artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts
}

func copyEvidenceOutputs(context runContext, scenario scenarioConfig) ([]outputArtifact, error) {
	seen := make(map[string]bool)
	var outputs []outputArtifact
	if err := copyEvidenceOutputDir(context, "outputs", seen, &outputs); err != nil {
		return nil, err
	}
	for _, path := range append([]string{scenario.ExpectedOutputs.ScenarioResult}, scenario.ExpectedOutputs.Files...) {
		if err := copyEvidenceOutputPath(context, path, seen, &outputs); err != nil {
			return nil, err
		}
	}
	for _, validator := range scenario.Validators {
		if validator.Path == "" {
			continue
		}
		if err := copyEvidenceOutputPath(context, validator.Path, seen, &outputs); err != nil {
			return nil, err
		}
	}
	sort.Slice(outputs, func(i, j int) bool {
		return outputs[i].Path < outputs[j].Path
	})
	return outputs, nil
}

func copyEvidenceOutputPath(context runContext, relativePath string, seen map[string]bool, outputs *[]outputArtifact) error {
	if relativePath == "" {
		return nil
	}
	clean, err := cleanRepoRelativePath(relativePath)
	if err != nil {
		return nil
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(context.Workspace, clean); err != nil {
		return nil
	}
	source := filepath.Join(context.Workspace, clean)
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if info.IsDir() {
		return copyEvidenceOutputDir(context, clean, seen, outputs)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return copyEvidenceOutputFile(context, source, clean, seen, outputs)
}

func copyEvidenceOutputDir(context runContext, relativePath string, seen map[string]bool, outputs *[]outputArtifact) error {
	source := filepath.Join(context.Workspace, relativePath)
	info, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyEvidenceOutputFile(context, path, filepath.Join(relativePath, relative), seen, outputs)
	})
}

func copyEvidenceOutputFile(context runContext, source string, relativePath string, seen map[string]bool, outputs *[]outputArtifact) error {
	clean := filepath.ToSlash(filepath.Clean(relativePath))
	if isReservedEvidenceArtifactPath(clean) {
		return nil
	}
	if seen[clean] {
		return nil
	}
	destination := filepath.Join(context.EvidenceDir, filepath.FromSlash(clean))
	if err := copyFile(source, destination); err != nil {
		return err
	}
	seen[clean] = true
	*outputs = append(*outputs, outputArtifact{Path: clean, MediaType: mediaTypeForPath(clean)})
	return nil
}

func isReservedEvidenceArtifactPath(path string) bool {
	slashed := strings.ToLower(filepath.ToSlash(path))
	for _, reserved := range []string{
		"evidence.json",
		"manifest.json",
		"events.jsonl",
		"scenario-result.json",
		"artifact-manifest.json",
		"review-comment.md",
		"logs",
		"screenshots",
		"recordings",
	} {
		if slashed == reserved || strings.HasPrefix(slashed, reserved+"/") {
			return true
		}
	}
	return false
}

func discoverVisualEvidence(evidenceDir string) ([]visualEvidenceArtifact, error) {
	var artifacts []visualEvidenceArtifact
	for _, spec := range []struct {
		dir       string
		kind      string
		mediaType string
	}{
		{dir: "screenshots", kind: "screenshot"},
		{dir: "recordings", kind: "recording", mediaType: "video/mp4"},
	} {
		root := filepath.Join(evidenceDir, spec.dir)
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil || entry.IsDir() {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			relative, err := filepath.Rel(evidenceDir, path)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, visualEvidenceArtifact{
				Kind:      spec.kind,
				Path:      filepath.ToSlash(relative),
				MediaType: visualEvidenceMediaType(spec.mediaType, relative),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("discover %s Visual Evidence: %w", spec.kind, err)
		}
	}
	return artifacts, nil
}

func visualEvidenceMediaType(defaultType string, relativePath string) string {
	if defaultType != "" {
		return defaultType
	}
	return mediaTypeForPath(relativePath)
}

func repoRelativePath(repoDir string, path string) string {
	relative, err := filepath.Rel(repoDir, path)
	if err != nil {
		return path
	}
	return relative
}

func appendEvent(path string, event string, detail string) error {
	record := map[string]string{
		"event":  event,
		"detail": detail,
	}
	content, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(content, '\n'))
	return err
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func decodeJSONUseNumber(content []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

type fakeHost struct{}

func (fakeHost) StagePayload(context runContext, payload []manifestPayload) error {
	for _, item := range payload {
		if err := ensurePayloadPathHasNoSymlinkAncestor(context.RepoDir, item.Source); err != nil {
			return fmt.Errorf("stage %s payload %s: %w", item.Kind, item.Source, err)
		}
		source := filepath.Join(context.RepoDir, item.Source)
		destination := filepath.Join(context.StateDir, item.Staged)
		if err := copyPath(source, destination); err != nil {
			return fmt.Errorf("stage %s payload %s: %w", item.Kind, item.Source, err)
		}
	}
	return nil
}

func ensurePayloadPathHasNoSymlinkAncestor(repoDir string, relativePath string) error {
	current := repoDir
	for _, part := range strings.Split(filepath.ToSlash(relativePath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("payload path %s must not be or contain a symlink", current)
		}
	}
	return nil
}

type fakeSessionBroker struct {
	Result ScenarioResult
}

func (broker fakeSessionBroker) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	log := fmt.Sprintf("fake Session Broker ran Scenario for Maya %s\n", scenario.MayaVersion)
	if err := os.WriteFile(context.LogPath, []byte(log), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", broker.Result.Status); err != nil {
		return ScenarioResult{}, err
	}
	return broker.Result, nil
}

func (fakeSessionBroker) CaptureScreenshot(context runContext, request screenshotRequest) (visualEvidenceArtifact, error) {
	name := request.Name
	if name == "" {
		name = "screenshot.png"
	}
	name = filepath.Base(filepath.ToSlash(name))
	if name == "." || name == ".." || name == "" {
		name = "screenshot.png"
	}
	relative := filepath.Join("screenshots", name)
	path := filepath.Join(context.EvidenceDir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := os.WriteFile(path, []byte("fake screenshot\n"), 0o644); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.screenshot.captured", filepath.ToSlash(relative)); err != nil {
		return visualEvidenceArtifact{}, err
	}
	return visualEvidenceArtifact{Kind: "screenshot", Path: filepath.ToSlash(relative), MediaType: "image/png"}, nil
}

func (fakeSessionBroker) CaptureRecording(context runContext, request recordingRequest) (visualEvidenceArtifact, error) {
	name := request.Name
	if name == "" {
		name = "recording.mp4"
	}
	name = filepath.Base(filepath.ToSlash(name))
	if name == "." || name == ".." || name == "" {
		name = "recording.mp4"
	}
	relative := filepath.Join("recordings", name)
	path := filepath.Join(context.EvidenceDir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return visualEvidenceArtifact{}, err
	}
	content := fmt.Sprintf("fake recording duration=%s fps=%d\n", request.Duration, request.FPS)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.recording.captured", filepath.ToSlash(relative)); err != nil {
		return visualEvidenceArtifact{}, err
	}
	return visualEvidenceArtifact{Kind: "recording", Path: filepath.ToSlash(relative), MediaType: "video/mp4"}, nil
}

type invalidSessionBroker struct {
	err error
}

func (broker invalidSessionBroker) RunScenario(runContext, scenarioConfig) (ScenarioResult, error) {
	return ScenarioResult{}, broker.err
}

func (broker invalidSessionBroker) CaptureScreenshot(runContext, screenshotRequest) (visualEvidenceArtifact, error) {
	return visualEvidenceArtifact{}, broker.err
}

func (broker invalidSessionBroker) CaptureRecording(runContext, recordingRequest) (visualEvidenceArtifact, error) {
	return visualEvidenceArtifact{}, broker.err
}

func sessionBrokerDisplayName(broker sessionBroker) string {
	switch broker.(type) {
	case ggMayaSessiondBroker:
		return "gg_mayasessiond Session Broker"
	default:
		return "fake Session Broker"
	}
}

func copyPath(source string, destination string) error {
	linkInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("payload path must not be a symlink")
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("payload path must not contain symlink %s", path)
			}
			if !entry.IsDir() {
				entryInfo, err := entry.Info()
				if err != nil {
					return err
				}
				if !entryInfo.Mode().IsRegular() {
					return fmt.Errorf("payload path %s must be a regular file", path)
				}
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			target := filepath.Join(destination, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return copyFile(path, target)
		})
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("payload path %s must be a regular file", source)
	}
	return copyFile(source, destination)
}

func copyFile(source string, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, input)
	return err
}
