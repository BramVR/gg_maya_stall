package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const runLedgerSchemaVersion = 1
const runLedgerEventsFileName = "events.jsonl"
const runLedgerLogPath = "logs/session.log"

const defaultRunLedgerRetention = 30 * 24 * time.Hour
const defaultRunLedgerMaxEvents = 10_000
const defaultRunLedgerMaxEventBytes = 8 * 1024 * 1024
const defaultRunLedgerMaxLogBytes = 1024 * 1024
const maximumRunLedgerMaxEvents = 100_000
const maximumRunLedgerMaxEventBytes = 64 * 1024 * 1024
const maximumRunLedgerMaxLogBytes = 64 * 1024 * 1024
const maximumRetainedRunLedgerEventBytes = 256 * 1024

type runLedgerConfig struct {
	Retention     string `yaml:"retention"`
	MaxEvents     int    `yaml:"maxEvents"`
	MaxEventBytes int    `yaml:"maxEventBytes"`
	MaxLogBytes   int    `yaml:"maxLogBytes"`
}

type runLedgerPolicy struct {
	Retention     time.Duration
	MaxEvents     int
	MaxEventBytes int
	MaxLogBytes   int
}

type runLedgerRecord struct {
	Version         int    `json:"version"`
	RunID           string `json:"runId"`
	Scenario        string `json:"scenario"`
	TargetProfile   string `json:"targetProfile,omitempty"`
	Host            string `json:"host,omitempty"`
	State           string `json:"state"`
	Status          string `json:"status,omitempty"`
	AcceptedAt      string `json:"acceptedAt"`
	UpdatedAt       string `json:"updatedAt"`
	CompletedAt     string `json:"completedAt,omitempty"`
	EvidenceDir     string `json:"evidenceDir"`
	Events          string `json:"events"`
	Log             string `json:"log"`
	EventCount      int    `json:"eventCount"`
	EventBytes      int    `json:"eventBytes"`
	EventsOmitted   int    `json:"eventsOmitted"`
	EventsTruncated bool   `json:"eventsTruncated"`
	LogBytes        int    `json:"logBytes"`
	LogTruncated    bool   `json:"logTruncated"`
	StopPhase       string `json:"stopPhase,omitempty"`
}

type runHistoryResponse struct {
	Version            int               `json:"version"`
	Kind               string            `json:"kind,omitempty"`
	Runs               []runLedgerRecord `json:"runs"`
	RunsOmitted        int               `json:"runsOmitted,omitempty"`
	RunsOmittedAtLeast int               `json:"runsOmittedAtLeast,omitempty"`
	RunsTruncated      bool              `json:"runsTruncated,omitempty"`
	NextBeforeRunID    string            `json:"nextBeforeRunId,omitempty"`
}

type historyOptions struct {
	JSON                 bool
	Scenario             string
	Host                 string
	State                string
	Since                string
	BeforeRunID          string
	ControlPlane         string
	ControlPlaneTokenEnv string
}

func parseHistoryArgs(args []string) (historyOptions, error) {
	var options historyOptions
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "--json":
			options.JSON = true
		case "--scenario", "--host", "--state", "--since", "--before-run", "--control-plane", "--control-plane-token-env":
			index++
			if index >= len(args) || args[index] == "" || len(args[index]) >= 2 && args[index][:2] == "--" {
				return historyOptions{}, newUsageError("%s needs a value", arg)
			}
			switch arg {
			case "--scenario":
				options.Scenario = args[index]
			case "--host":
				options.Host = args[index]
			case "--state":
				options.State = args[index]
			case "--since":
				options.Since = args[index]
			case "--before-run":
				if err := validateRunID(args[index]); err != nil {
					return historyOptions{}, newUsageError("--before-run needs a valid Run ID")
				}
				options.BeforeRunID = args[index]
			case "--control-plane":
				options.ControlPlane = args[index]
			case "--control-plane-token-env":
				options.ControlPlaneTokenEnv = args[index]
			}
		default:
			return historyOptions{}, newUsageError("unknown history option %q", arg)
		}
	}
	return options, nil
}

func initializeRunLedger(repoDir string, manifest runManifest, acceptedAt time.Time, sourceEventsPath string) error {
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs", manifest.RunID)); err != nil {
		return err
	}
	record := runLedgerRecord{
		Version:       runLedgerSchemaVersion,
		RunID:         manifest.RunID,
		Scenario:      manifest.Scenario,
		TargetProfile: manifest.TargetProfile,
		Host:          manifest.Host,
		State:         "submitted",
		AcceptedAt:    acceptedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     acceptedAt.UTC().Format(time.RFC3339Nano),
		EvidenceDir:   filepath.ToSlash(filepath.Join("artifacts", "maya-stall", manifest.RunID)),
		Events:        runLedgerEventsFileName,
		Log:           runLedgerLogPath,
		EventCount:    1,
	}
	if err := os.MkdirAll(runLedgerRoot(repoDir), 0o755); err != nil {
		return err
	}
	if err := syncRunLedgerDirectoryChain(runLedgerRoot(repoDir), repoDir); err != nil {
		return err
	}
	temporaryDir, err := os.MkdirTemp(runLedgerRoot(repoDir), "."+manifest.RunID+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temporaryDir) }()
	temporaryEventsPath := filepath.Join(temporaryDir, runLedgerEventsFileName)
	if err := copySequencedEvents(sourceEventsPath, temporaryEventsPath, record.AcceptedAt); err != nil {
		return err
	}
	if err := syncRunLedgerFile(temporaryEventsPath); err != nil {
		return err
	}
	eventInfo, err := os.Stat(temporaryEventsPath)
	if err != nil {
		return err
	}
	eventCount, err := lastRunLedgerEventSequence(temporaryEventsPath)
	if err != nil {
		return err
	}
	record.EventCount = eventCount
	record.EventBytes = int(eventInfo.Size())
	if err := writeRunLedgerBytes(filepath.Join(temporaryDir, filepath.FromSlash(runLedgerLogPath)), nil); err != nil {
		return err
	}
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRunLedgerBytes(filepath.Join(temporaryDir, "run.json"), append(content, '\n')); err != nil {
		return err
	}
	if err := syncRunLedgerDirectory(temporaryDir); err != nil {
		return err
	}
	if err := os.Rename(temporaryDir, runLedgerDir(repoDir, manifest.RunID)); err != nil {
		return err
	}
	return syncRunLedgerDirectory(runLedgerRoot(repoDir))
}

func cleanupRunLedgerRecord(repoDir string, runID string) error {
	return withRunLedgerRootLock(repoDir, true, func() error {
		return cleanupRunLedgerRecordUnlocked(repoDir, runID)
	})
}

func cleanupRunLedgerRecordUnlocked(repoDir string, runID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	path := runLedgerDir(repoDir, runID)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("embedded run ledger path %s must be a directory, not a symlink", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return syncRunLedgerDirectory(runLedgerRoot(repoDir))
}

func defaultRunLedgerPolicy() runLedgerPolicy {
	return runLedgerPolicy{
		Retention:     defaultRunLedgerRetention,
		MaxEvents:     defaultRunLedgerMaxEvents,
		MaxEventBytes: defaultRunLedgerMaxEventBytes,
		MaxLogBytes:   defaultRunLedgerMaxLogBytes,
	}
}

func availableRunLedgerPolicy(repoDir string) (runLedgerPolicy, bool) {
	config, _, err := loadRepoRunConfig(repoDir)
	if errors.Is(err, errRepoRunConfigNotFound) {
		return defaultRunLedgerPolicy(), true
	}
	if err != nil {
		return runLedgerPolicy{}, false
	}
	policy, err := resolveRunLedgerPolicy(config.RunLedger)
	if err != nil {
		return runLedgerPolicy{}, false
	}
	return policy, true
}

func resolveRunLedgerPolicy(config runLedgerConfig) (runLedgerPolicy, error) {
	policy := defaultRunLedgerPolicy()
	if config.Retention != "" {
		retention, err := time.ParseDuration(config.Retention)
		if err != nil || retention <= 0 {
			return runLedgerPolicy{}, fmt.Errorf("runLedger.retention must be a positive duration")
		}
		policy.Retention = retention
	}
	if config.MaxEvents != 0 {
		if config.MaxEvents < 3 || config.MaxEvents > maximumRunLedgerMaxEvents {
			return runLedgerPolicy{}, fmt.Errorf("runLedger.maxEvents must be between 3 and %d", maximumRunLedgerMaxEvents)
		}
		policy.MaxEvents = config.MaxEvents
	}
	if config.MaxEventBytes != 0 {
		if config.MaxEventBytes < 1024 || config.MaxEventBytes > maximumRunLedgerMaxEventBytes {
			return runLedgerPolicy{}, fmt.Errorf("runLedger.maxEventBytes must be between 1024 and %d", maximumRunLedgerMaxEventBytes)
		}
		policy.MaxEventBytes = config.MaxEventBytes
	}
	if config.MaxLogBytes != 0 {
		if config.MaxLogBytes < 96 || config.MaxLogBytes > maximumRunLedgerMaxLogBytes {
			return runLedgerPolicy{}, fmt.Errorf("runLedger.maxLogBytes must be between 96 and %d", maximumRunLedgerMaxLogBytes)
		}
		policy.MaxLogBytes = config.MaxLogBytes
	}
	return policy, nil
}

func finalizeRunLedger(repoDir string, outcome runOutcome, manifest runManifest, policy runLedgerPolicy, now time.Time) error {
	return withRunLedgerLock(repoDir, manifest.RunID, func() error {
		return finalizeRunLedgerUnlocked(repoDir, outcome, manifest, policy, now)
	})
}

func finalizeRunLedgerUnlocked(repoDir string, outcome runOutcome, manifest runManifest, policy runLedgerPolicy, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, manifest.RunID)
	if err != nil {
		return err
	}
	record.Scenario = outcome.Scenario
	record.TargetProfile = outcome.TargetProfile
	record.Host = outcome.Host
	record.Status = outcome.Result.Status
	record.State = terminalRunLedgerState(outcome)
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if record.State != "kept" && record.State != "submitted" {
		record.CompletedAt = record.UpdatedAt
	}
	if err := syncRunLedgerArtifacts(repoDir, &record, policy); err != nil {
		return err
	}
	return writeRunLedgerRecord(repoDir, record)
}

func syncRunLedgerArtifacts(repoDir string, record *runLedgerRecord, policy runLedgerPolicy) error {
	for _, relativePath := range []string{runLedgerEventsFileName, runLedgerLogPath} {
		if err := ensureWorkspacePathHasNoSymlinkAncestor(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(relativePath)); err != nil {
			return fmt.Errorf("validate embedded run ledger artifact path %s: %w", relativePath, err)
		}
	}
	evidenceDir := filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir))
	eventsDestination := filepath.Join(runLedgerDir(repoDir, record.RunID), runLedgerEventsFileName)
	eventsSource, copyEvents, err := selectRunLedgerArtifactSource(
		repoDir,
		filepath.Join(repoDir, ".maya-stall", "state", "runs", record.RunID, "events.jsonl"),
		filepath.Join(evidenceDir, evidenceEventsFileName),
		eventsDestination,
		"events",
	)
	if err != nil {
		return err
	}
	if copyEvents {
		eventCount, eventsOmitted, eventsTruncated, eventBytes, err := copyBoundedLedgerEvents(eventsSource, eventsDestination, policy.MaxEvents, policy.MaxEventBytes, record.AcceptedAt)
		if err != nil {
			return fmt.Errorf("retain %s in embedded run ledger: %w", evidenceEventsFileName, err)
		}
		record.EventCount = eventCount
		record.EventBytes = eventBytes
		record.EventsOmitted = eventsOmitted
		record.EventsTruncated = eventsTruncated
	} else {
		eventCount, eventsOmitted, eventsTruncated, eventBytes, err := readRetainedRunLedgerEventMetadata(eventsDestination)
		if err != nil {
			return fmt.Errorf("read retained event metadata: %w", err)
		}
		record.EventCount = eventCount
		record.EventBytes = eventBytes
		record.EventsOmitted = eventsOmitted
		record.EventsTruncated = eventsTruncated
		if eventCount > policy.MaxEvents || eventBytes > policy.MaxEventBytes {
			eventCount, eventsOmitted, eventsTruncated, eventBytes, err = copyBoundedLedgerEvents(eventsDestination, eventsDestination, policy.MaxEvents, policy.MaxEventBytes, record.AcceptedAt)
			if err != nil {
				return fmt.Errorf("re-bound retained events: %w", err)
			}
			record.EventCount = eventCount
			record.EventBytes = eventBytes
			record.EventsOmitted = eventsOmitted
			record.EventsTruncated = eventsTruncated
		}
	}

	logDestination := filepath.Join(runLedgerDir(repoDir, record.RunID), filepath.FromSlash(runLedgerLogPath))
	logSource, copyLog, err := selectRunLedgerArtifactSource(
		repoDir,
		filepath.Join(repoDir, ".maya-stall", "state", "runs", record.RunID, filepath.FromSlash(evidenceLogPath)),
		filepath.Join(evidenceDir, filepath.FromSlash(evidenceLogPath)),
		logDestination,
		"log",
	)
	if err != nil {
		return err
	}
	if copyLog {
		logBytes, truncated, err := copyBoundedLedgerLog(logSource, logDestination, policy.MaxLogBytes)
		if err != nil {
			return fmt.Errorf("retain %s in embedded run ledger: %w", evidenceLogPath, err)
		}
		record.LogBytes = logBytes
		record.LogTruncated = truncated
	} else {
		logInfo, err := os.Stat(logDestination)
		if err != nil {
			return fmt.Errorf("read retained log metadata: %w", err)
		}
		_, truncated, err := retainedLedgerLogSourceBytesAndTruncated(logDestination)
		if err != nil {
			return fmt.Errorf("read retained log metadata: %w", err)
		}
		record.LogBytes = int(logInfo.Size())
		record.LogTruncated = truncated
		if logInfo.Size() > int64(policy.MaxLogBytes) {
			logBytes, truncated, err := copyBoundedLedgerLog(logDestination, logDestination, policy.MaxLogBytes)
			if err != nil {
				return fmt.Errorf("re-bound retained log: %w", err)
			}
			record.LogBytes = logBytes
			record.LogTruncated = truncated
		}
	}
	return nil
}

func readRetainedRunLedgerEventMetadata(path string) (int, int, bool, int, error) {
	file, err := openRunLedgerRead(path)
	if err != nil {
		return 0, 0, false, 0, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return 0, 0, false, 0, err
	}
	reader := bufio.NewReaderSize(file, 64*1024)
	count := 0
	omitted := 0
	truncated := false
	for {
		line, readErr := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			count++
			var event map[string]any
			if err := json.Unmarshal(trimmed, &event); err != nil {
				return 0, 0, false, 0, err
			}
			eventType, _ := event["type"].(string)
			if eventType == "run-ledger.event.truncated" {
				truncated = true
			}
			if eventType == "run-ledger.events.truncated" {
				truncated = true
				if details, ok := event["details"].(map[string]any); ok {
					switch value := details["omittedCount"].(type) {
					case float64:
						omitted += int(value)
					case json.Number:
						parsed, _ := value.Int64()
						omitted += int(parsed)
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, 0, false, 0, readErr
		}
	}
	return count, omitted, truncated, int(info.Size()), nil
}

func selectRunLedgerArtifactSource(repoDir string, primary string, fallback string, destination string, kind string) (string, bool, error) {
	primaryInfo, found, err := validateRunLedgerArtifactSource(repoDir, primary)
	if err != nil {
		return "", false, err
	}
	if found {
		switch kind {
		case "events":
			primarySequence, err := lastRunLedgerEventSequence(primary)
			if err != nil {
				destinationInfo, destinationErr := os.Lstat(destination)
				if destinationErr == nil && destinationInfo.Mode().IsRegular() && destinationInfo.Mode()&os.ModeSymlink == 0 {
					if _, destinationErr := lastRunLedgerEventSequence(destination); destinationErr == nil {
						return "", false, nil
					}
				}
				return "", false, fmt.Errorf("validate transient run events: %w", err)
			}
			destinationInfo, destinationErr := os.Lstat(destination)
			if destinationErr == nil && destinationInfo.Mode().IsRegular() && destinationInfo.Mode()&os.ModeSymlink == 0 {
				if destinationSequence, destinationErr := lastRunLedgerEventSequence(destination); destinationErr == nil && destinationSequence >= primarySequence {
					return "", false, nil
				}
			}
		case "log":
			destinationInfo, destinationErr := os.Lstat(destination)
			if destinationErr == nil && destinationInfo.Mode().IsRegular() && destinationInfo.Mode()&os.ModeSymlink == 0 {
				if destinationSourceBytes, destinationErr := retainedLedgerLogSourceBytes(destination); destinationErr == nil && destinationSourceBytes >= primaryInfo.Size() {
					return "", false, nil
				}
			}
		}
		return primary, true, nil
	}
	_, found, err = validateRunLedgerArtifactSource(repoDir, fallback)
	if err != nil || !found {
		return "", false, err
	}
	destinationInfo, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return fallback, true, nil
	}
	if err != nil {
		return "", false, err
	}
	if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.Mode().IsRegular() {
		return "", false, fmt.Errorf("embedded run ledger artifact %s must be a regular file, not a symlink", destination)
	}
	newer, err := runLedgerFallbackIsNewer(fallback, destination, kind)
	return fallback, newer, err
}

func runLedgerFallbackIsNewer(fallback string, destination string, kind string) (bool, error) {
	switch kind {
	case "events":
		fallbackSequence, err := lastRunLedgerEventSequence(fallback)
		if err != nil {
			return false, err
		}
		destinationSequence, err := lastRunLedgerEventSequence(destination)
		if err != nil {
			return false, err
		}
		return fallbackSequence > destinationSequence, nil
	case "log":
		fallbackInfo, err := os.Stat(fallback)
		if err != nil {
			return false, err
		}
		destinationSourceBytes, err := retainedLedgerLogSourceBytes(destination)
		if err != nil {
			return false, err
		}
		return fallbackInfo.Size() > destinationSourceBytes, nil
	default:
		return false, fmt.Errorf("unknown embedded run ledger artifact kind %q", kind)
	}
}

func lastRunLedgerEventSequence(path string) (int, error) {
	file, err := openRunLedgerRead(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReaderSize(file, 64*1024)
	lastSequence := 0
	lineNumber := 0
	for {
		line, found, _, readErr := readBoundedLedgerEvent(reader, lineNumber+1, maximumRetainedRunLedgerEventBytes, time.Unix(0, 0).UTC().Format(time.RFC3339Nano))
		if found {
			lineNumber++
			var event map[string]any
			if err := json.Unmarshal(line, &event); err != nil {
				return 0, err
			}
			if sequence := ledgerEventSequence(event); sequence > lastSequence {
				lastSequence = sequence
			}
		}
		if errors.Is(readErr, io.EOF) {
			return lastSequence, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

func retainedLedgerLogSourceBytes(path string) (int64, error) {
	sourceBytes, _, err := retainedLedgerLogSourceBytesAndTruncated(path)
	return sourceBytes, err
}

func retainedLedgerLogSourceBytesAndTruncated(path string) (int64, bool, error) {
	file, err := openRunLedgerRead(path)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return 0, false, err
	}
	reader := bufio.NewReaderSize(file, 256)
	line, readErr := reader.ReadString('\n')
	if errors.Is(readErr, bufio.ErrBufferFull) || errors.Is(readErr, io.EOF) {
		return info.Size(), false, nil
	}
	if readErr != nil {
		return 0, false, readErr
	}
	var omitted int64
	trimmed := strings.TrimSuffix(line, "\n")
	if _, err := fmt.Sscanf(trimmed, "[maya-stall: log truncated; omitted %d bytes]", &omitted); err != nil || omitted < 0 || trimmed != fmt.Sprintf("[maya-stall: log truncated; omitted %d bytes]", omitted) {
		return info.Size(), false, nil
	}
	return omitted + info.Size() - int64(len(line)), true, nil
}

func validateRunLedgerArtifactSource(repoDir string, path string) (os.FileInfo, bool, error) {
	relative, err := filepath.Rel(repoDir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, false, fmt.Errorf("run ledger artifact source %s must be inside repo %s", path, repoDir)
	}
	if err := ensureWorkspacePathHasNoSymlinkAncestor(repoDir, relative); err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("run ledger artifact source %s must be a regular file, not a symlink", path)
	}
	return info, true, nil
}

func copyBoundedLedgerEvents(source string, destination string, maxEvents int, maxBytes int, fallbackTimestamps ...string) (int, int, bool, int, error) {
	fallbackTimestamp := time.Now().UTC().Format(time.RFC3339Nano)
	if len(fallbackTimestamps) > 0 && fallbackTimestamps[0] != "" {
		fallbackTimestamp = fallbackTimestamps[0]
	}
	file, err := openRunLedgerRead(source)
	if err != nil {
		return 0, 0, false, 0, err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReaderSize(file, 64*1024)
	maxLineBytes := maxBytes / 2
	if maxLineBytes > maximumRetainedRunLedgerEventBytes {
		maxLineBytes = maximumRetainedRunLedgerEventBytes
	}
	var first []byte
	tail := make([][]byte, 0)
	tailHead := 0
	tailBytes := 0
	total := 0
	contentTruncated := false
	evictOldest := func() {
		tailBytes -= len(tail[tailHead]) + 1
		tail[tailHead] = nil
		tailHead++
		if tailHead > 1024 && tailHead*2 >= len(tail) {
			tail = append([][]byte(nil), tail[tailHead:]...)
			tailHead = 0
		}
	}
	for {
		retained, found, replaced, readErr := readBoundedLedgerEvent(reader, total+1, maxLineBytes, fallbackTimestamp)
		if found {
			contentTruncated = contentTruncated || replaced
			if previouslyOmitted, ok := runLedgerOmittedEventCount(retained); ok {
				total += previouslyOmitted
				contentTruncated = true
			} else if first == nil {
				total++
				first = retained
			} else {
				total++
				tail = append(tail, retained)
				tailBytes += len(retained) + 1
				for len(tail)-tailHead > maxEvents-1 || len(first)+1+tailBytes > maxBytes {
					if tailHead >= len(tail) {
						return 0, 0, false, 0, fmt.Errorf("embedded run ledger event limit is too small for retained event metadata")
					}
					evictOldest()
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, 0, false, 0, readErr
		}
	}
	lines := make([][]byte, 0)
	if first != nil {
		lines = append(lines, first)
	}
	activeTail := tail[tailHead:]
	omitted := total - len(activeTail) - len(lines)
	if omitted > 0 {
		var firstEvent map[string]any
		if err := json.Unmarshal(first, &firstEvent); err != nil {
			return 0, 0, false, 0, err
		}
		firstSequence := ledgerEventSequence(firstEvent)
		var encodedMarker []byte
		for {
			marker := map[string]any{
				"event":     "run-ledger.events.truncated",
				"sequence":  firstSequence + 1,
				"timestamp": firstEvent["timestamp"],
				"phase":     "retention",
				"type":      "run-ledger.events.truncated",
				"stream":    "lifecycle",
				"details": map[string]any{
					"omittedCount":         omitted,
					"firstOmittedSequence": firstSequence + 1,
					"lastOmittedSequence":  firstSequence + omitted,
				},
			}
			encodedMarker, err = json.Marshal(marker)
			if err != nil {
				return 0, 0, false, 0, err
			}
			if len(activeTail) <= maxEvents-2 && len(first)+len(encodedMarker)+tailBytes+2 <= maxBytes {
				break
			}
			if len(activeTail) == 0 {
				return 0, 0, false, 0, fmt.Errorf("embedded run ledger event limit is too small for truncation metadata")
			}
			evictOldest()
			activeTail = tail[tailHead:]
			omitted++
		}
		lines = append(lines, encodedMarker)
	}
	lines = append(lines, activeTail...)
	var bounded bytes.Buffer
	for _, line := range lines {
		bounded.Write(line)
		bounded.WriteByte('\n')
	}
	if err := writeRunLedgerBytes(destination, bounded.Bytes()); err != nil {
		return 0, 0, false, 0, err
	}
	return len(lines), omitted, omitted > 0 || contentTruncated, bounded.Len(), nil
}

func runLedgerOmittedEventCount(encoded []byte) (int, bool) {
	var event map[string]any
	if err := json.Unmarshal(encoded, &event); err != nil || event["type"] != "run-ledger.events.truncated" {
		return 0, false
	}
	details, ok := event["details"].(map[string]any)
	if !ok {
		return 0, false
	}
	switch value := details["omittedCount"].(type) {
	case float64:
		if value < 0 || value != float64(int(value)) {
			return 0, false
		}
		return int(value), true
	case json.Number:
		count, err := value.Int64()
		if err != nil || count < 0 {
			return 0, false
		}
		return int(count), true
	case int:
		return value, value >= 0
	default:
		return 0, false
	}
}

func readBoundedLedgerEvent(reader *bufio.Reader, sequence int, maxBytes int, fallbackTimestamp string) ([]byte, bool, bool, error) {
	line := make([]byte, 0)
	originalBytes := 0
	oversized := false
	for {
		fragment, err := reader.ReadSlice('\n')
		originalBytes += len(fragment)
		if !oversized {
			if len(line)+len(fragment) <= maxBytes {
				line = append(line, fragment...)
			} else {
				line = nil
				oversized = true
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, false, err
		}
		if originalBytes == 0 && errors.Is(err, io.EOF) {
			return nil, false, false, io.EOF
		}
		if oversized {
			marker, marshalErr := marshalTruncatedRunLedgerEvent(sequence, fallbackTimestamp, originalBytes)
			return marker, true, true, marshalErr
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			return nil, false, false, err
		}
		var event map[string]any
		if decodeErr := json.Unmarshal(trimmed, &event); decodeErr != nil {
			return nil, false, false, decodeErr
		}
		effectiveSequence := ledgerEventSequence(event)
		if effectiveSequence == 0 {
			effectiveSequence = sequence
		}
		event = normalizeRunLedgerEvent(event, effectiveSequence, fallbackTimestamp)
		encoded, encodeErr := json.Marshal(event)
		if encodeErr != nil {
			return nil, false, false, encodeErr
		}
		if len(encoded) > maxBytes {
			marker, marshalErr := marshalTruncatedRunLedgerEvent(effectiveSequence, fallbackTimestamp, originalBytes)
			return marker, true, true, marshalErr
		}
		return encoded, true, false, err
	}
}

func marshalTruncatedRunLedgerEvent(sequence int, timestamp string, originalBytes int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event":     "run-ledger.event.truncated",
		"sequence":  sequence,
		"timestamp": timestamp,
		"phase":     "retention",
		"type":      "run-ledger.event.truncated",
		"stream":    "lifecycle",
		"details": map[string]any{
			"originalBytes": originalBytes,
		},
	})
}

func normalizeRunLedgerEvent(event map[string]any, sequence int, fallbackTimestamp string) map[string]any {
	eventType, _ := event["type"].(string)
	if eventType == "" {
		eventType, _ = event["event"].(string)
	}
	if eventType == "" {
		eventType = "run.event"
	}
	timestamp, _ := event["timestamp"].(string)
	if timestamp == "" {
		timestamp = fallbackTimestamp
	}
	phase, _ := event["phase"].(string)
	if phase == "" {
		phase = runEventPhase(eventType)
	}
	stream, _ := event["stream"].(string)
	if stream == "" {
		stream = "lifecycle"
	}
	details, _ := event["details"].(map[string]any)
	if details == nil {
		details = make(map[string]any)
	}
	if detail, ok := event["detail"].(string); ok {
		if _, exists := details["message"]; !exists {
			details["message"] = detail
		}
	}
	event["event"] = eventType
	event["sequence"] = sequence
	event["timestamp"] = timestamp
	event["phase"] = phase
	event["type"] = eventType
	event["stream"] = stream
	event["details"] = details
	return event
}

func ledgerEventSequence(event map[string]any) int {
	switch value := event["sequence"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		sequence, _ := value.Int64()
		return int(sequence)
	default:
		return 0
	}
}

func copyBoundedLedgerLog(source string, destination string, maxBytes int) (int, bool, error) {
	sourceBytes, sourceTruncated, err := retainedLedgerLogSourceBytesAndTruncated(source)
	if err != nil {
		return 0, false, err
	}
	file, err := openRunLedgerRead(source)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return 0, false, err
	}
	availableTailBytes := info.Size()
	if sourceTruncated {
		reader := bufio.NewReaderSize(file, 256)
		marker, readErr := reader.ReadString('\n')
		if readErr != nil {
			return 0, false, readErr
		}
		availableTailBytes -= int64(len(marker))
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return 0, false, err
		}
	}
	truncated := sourceTruncated || sourceBytes > int64(maxBytes)
	var content []byte
	if truncated {
		marker := []byte(fmt.Sprintf("[maya-stall: log truncated; omitted %d bytes]\n", sourceBytes-int64(maxBytes)))
		for {
			tailBytes := maxBytes - len(marker)
			if int64(tailBytes) > availableTailBytes {
				tailBytes = int(availableTailBytes)
			}
			omitted := sourceBytes - int64(tailBytes)
			updated := []byte(fmt.Sprintf("[maya-stall: log truncated; omitted %d bytes]\n", omitted))
			if len(updated) == len(marker) {
				marker = updated
				if _, err := file.Seek(-int64(tailBytes), io.SeekEnd); err != nil {
					return 0, false, err
				}
				tail := make([]byte, tailBytes)
				if _, err := io.ReadFull(file, tail); err != nil {
					return 0, false, err
				}
				content = append(marker, tail...)
				break
			}
			marker = updated
		}
	} else {
		content, err = io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
		if err != nil {
			return 0, false, err
		}
	}
	if err := writeRunLedgerBytes(destination, content); err != nil {
		return 0, false, err
	}
	return len(content), truncated, nil
}

func writeRunLedgerBytes(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := rejectExistingFileLeaf(path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncRunLedgerDirectory(filepath.Dir(path))
}

func syncRunLedgerFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncRunLedgerDirectoryChain(path string, repoDir string) error {
	current := filepath.Clean(path)
	stop := filepath.Clean(repoDir)
	relative, err := filepath.Rel(stop, current)
	if err != nil || relative == ".." || len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator) {
		return fmt.Errorf("embedded run ledger path %s must be inside repo %s", path, repoDir)
	}
	for {
		if err := syncRunLedgerDirectory(current); err != nil {
			return err
		}
		if current == stop {
			return nil
		}
		current = filepath.Dir(current)
	}
}

func terminalRunLedgerState(outcome runOutcome) string {
	if outcome.StopPolicy == "kept" {
		return "kept"
	}
	if outcome.StopPolicy == "unresolved" || outcome.Failure != nil && outcome.Failure.CleanupState == "failed" {
		return "cleanup-failed"
	}
	if outcome.Result.Status == resultStatusPassed {
		return "completed"
	}
	return "failed"
}

func runLedgerRoot(repoDir string) string {
	return filepath.Join(repoDir, ".maya-stall", "state", "ledger", "runs")
}

func runLedgerDir(repoDir string, runID string) string {
	return filepath.Join(runLedgerRoot(repoDir), runID)
}

func writeRunLedgerRecord(repoDir string, record runLedgerRecord) error {
	if err := validateRunID(record.RunID); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs", record.RunID)); err != nil {
		return err
	}
	content, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return writeRunLedgerBytes(filepath.Join(runLedgerDir(repoDir, record.RunID), "run.json"), append(content, '\n'))
}

func readRunLedgerRecord(repoDir string, runID string) (runLedgerRecord, error) {
	if err := validateRunID(runID); err != nil {
		return runLedgerRecord{}, err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs", runID)); err != nil {
		return runLedgerRecord{}, err
	}
	path := filepath.Join(runLedgerDir(repoDir, runID), "run.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return runLedgerRecord{}, newUsageError("run %q not found", runID)
	}
	if err != nil {
		return runLedgerRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return runLedgerRecord{}, fmt.Errorf("embedded run ledger record %s must be a regular file, not a symlink", runID)
	}
	content, err := readRunLedgerBytes(path)
	if err != nil {
		return runLedgerRecord{}, err
	}
	var record runLedgerRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return runLedgerRecord{}, fmt.Errorf("parse embedded run ledger record %s: %w", runID, err)
	}
	if record.Version != runLedgerSchemaVersion {
		return runLedgerRecord{}, fmt.Errorf("embedded run ledger record %s has unsupported version %d", runID, record.Version)
	}
	if record.RunID != runID {
		return runLedgerRecord{}, fmt.Errorf("embedded run ledger record %s identifies run %q", runID, record.RunID)
	}
	if _, err := time.Parse(time.RFC3339Nano, record.AcceptedAt); err != nil {
		return runLedgerRecord{}, fmt.Errorf("embedded run ledger record %s has invalid acceptedAt: %w", runID, err)
	}
	expectedEvidenceDir := filepath.ToSlash(filepath.Join("artifacts", "maya-stall", runID))
	if record.Events != runLedgerEventsFileName || record.Log != runLedgerLogPath || record.EvidenceDir != expectedEvidenceDir {
		return runLedgerRecord{}, fmt.Errorf("embedded run ledger record %s has invalid artifact paths", runID)
	}
	return record, nil
}

func readRunLedgerBytes(path string) ([]byte, error) {
	file, err := openRunLedgerRead(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func listRunLedgerRecords(repoDir string) ([]runLedgerRecord, error) {
	var records []runLedgerRecord
	err := withRunLedgerRootLock(repoDir, false, func() error {
		var listErr error
		records, listErr = listRunLedgerRecordsUnlocked(repoDir)
		return listErr
	})
	return records, err
}

func listRunLedgerRecordsUnlocked(repoDir string, skipInvalid ...bool) ([]runLedgerRecord, error) {
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs")); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(runLedgerRoot(repoDir))
	if errors.Is(err, os.ErrNotExist) {
		return []runLedgerRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]runLedgerRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if entry.Name() != "" && entry.Name()[0] == '.' {
			continue
		}
		if err := validateRunID(entry.Name()); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(runLedgerRoot(repoDir), entry.Name(), "run.json")); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		record, err := readRunLedgerRecord(repoDir, entry.Name())
		if err != nil {
			if len(skipInvalid) > 0 && skipInvalid[0] {
				continue
			}
			return nil, err
		}
		records = append(records, record)
	}
	sortRunLedgerRecordsNewest(records)
	return records, nil
}

func sortRunLedgerRecordsNewest(records []runLedgerRecord) {
	sort.Slice(records, func(i int, j int) bool {
		left, _ := time.Parse(time.RFC3339Nano, records[i].AcceptedAt)
		right, _ := time.Parse(time.RFC3339Nano, records[j].AcceptedAt)
		if left.Equal(right) {
			leftOrdinal := runIDCollisionOrdinal(records[i].RunID)
			rightOrdinal := runIDCollisionOrdinal(records[j].RunID)
			if leftOrdinal != rightOrdinal {
				return leftOrdinal > rightOrdinal
			}
			return records[i].RunID > records[j].RunID
		}
		return left.After(right)
	})
}

func runMatchesHistoryOptions(record runLedgerRecord, options historyOptions, cutoff time.Time) (bool, error) {
	if options.Scenario != "" && record.Scenario != options.Scenario || options.Host != "" && record.Host != options.Host || options.State != "" && record.State != options.State {
		return false, nil
	}
	if cutoff.IsZero() {
		return true, nil
	}
	acceptedAt, err := time.Parse(time.RFC3339Nano, record.AcceptedAt)
	if err != nil {
		return false, fmt.Errorf("parse run acceptedAt for %s: %w", record.RunID, err)
	}
	return !acceptedAt.Before(cutoff), nil
}

func runIDCollisionOrdinal(runID string) int {
	separator := strings.LastIndexByte(runID, '-')
	if separator < 0 {
		return 0
	}
	ordinal, err := strconv.Atoi(runID[separator+1:])
	if err != nil || ordinal < 0 {
		return 0
	}
	return ordinal
}

func pruneRunLedger(repoDir string, policy runLedgerPolicy, now time.Time, currentRunID string) error {
	return withRunLedgerRootLock(repoDir, true, func() error {
		return pruneRunLedgerUnlocked(repoDir, policy, now, currentRunID)
	})
}

func pruneRunLedgerUnlocked(repoDir string, policy runLedgerPolicy, now time.Time, currentRunID string) error {
	records, err := listRunLedgerRecordsUnlocked(repoDir, true)
	if err != nil {
		return err
	}
	cutoff := now.UTC().Add(-policy.Retention)
	for _, record := range records {
		if record.RunID == currentRunID || record.State != "completed" && record.State != "failed" && record.State != "canceled" {
			continue
		}
		retainedAt := record.CompletedAt
		if retainedAt == "" {
			retainedAt = record.UpdatedAt
		}
		timestamp, err := time.Parse(time.RFC3339Nano, retainedAt)
		if err != nil {
			return fmt.Errorf("parse embedded run ledger retention time for %s: %w", record.RunID, err)
		}
		if !timestamp.Before(cutoff) {
			continue
		}
		path := runLedgerDir(repoDir, record.RunID)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("embedded run ledger path %s must be a directory, not a symlink", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove expired embedded run ledger record %s: %w", record.RunID, err)
		}
		if err := syncRunLedgerDirectory(runLedgerRoot(repoDir)); err != nil {
			return fmt.Errorf("persist expired embedded run ledger removal %s: %w", record.RunID, err)
		}
	}
	return nil
}

func printRunHistory(repoDir string, options historyOptions, now time.Time, stdout io.Writer) error {
	cutoff, err := historySinceCutoff(options.Since, now)
	if err != nil {
		return err
	}
	policy, retentionAvailable := availableRunLedgerPolicy(repoDir)
	if retentionAvailable {
		if err := pruneRunLedger(repoDir, policy, now, ""); err != nil {
			return fmt.Errorf("apply embedded run ledger retention: %w", err)
		}
	}
	records, err := listRunLedgerRecords(repoDir)
	if err != nil {
		return err
	}
	filtered := make([]runLedgerRecord, 0, len(records))
	for _, record := range records {
		if options.Scenario != "" && record.Scenario != options.Scenario ||
			options.Host != "" && record.Host != options.Host ||
			options.State != "" && record.State != options.State {
			continue
		}
		if !cutoff.IsZero() {
			acceptedAt, err := time.Parse(time.RFC3339Nano, record.AcceptedAt)
			if err != nil {
				return fmt.Errorf("parse embedded run ledger acceptedAt for %s: %w", record.RunID, err)
			}
			if acceptedAt.Before(cutoff) {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	records = filtered
	if options.JSON {
		return json.NewEncoder(stdout).Encode(runHistoryResponse{Version: runLedgerSchemaVersion, Runs: records})
	}
	if len(records) == 0 {
		_, err = fmt.Fprintln(stdout, "state: no runs")
		return err
	}
	for _, record := range records {
		if _, err := fmt.Fprintf(stdout, "run: %s\nscenario: %s\nhost: %s\nstate: %s\nstatus: %s\nacceptedAt: %s\n", record.RunID, record.Scenario, record.Host, record.State, record.Status, record.AcceptedAt); err != nil {
			return err
		}
	}
	return nil
}

func historySinceCutoff(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < 0 {
			return time.Time{}, newUsageError("--since duration must not be negative")
		}
		return now.UTC().Add(-duration), nil
	}
	cutoff, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, newUsageError("--since needs a duration or RFC3339 timestamp")
	}
	return cutoff.UTC(), nil
}

func attachLedgerRun(repoDir string, runID string, stdout io.Writer) error {
	return withRunLedgerRootLock(repoDir, false, func() error {
		return attachLedgerRunUnlocked(repoDir, runID, stdout)
	})
}

func attachLedgerRunUnlocked(repoDir string, runID string, stdout io.Writer) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	for _, relativePath := range []string{record.Events, record.Log} {
		if err := ensureWorkspacePathHasNoSymlinkAncestor(runLedgerDir(repoDir, runID), filepath.FromSlash(relativePath)); err != nil {
			return fmt.Errorf("validate embedded run ledger artifact path %s: %w", relativePath, err)
		}
	}
	if _, err := fmt.Fprintf(stdout, "run: %s\nstate: %s\nevents:\n", record.RunID, record.State); err != nil {
		return err
	}
	if err := copyTextFile(filepath.Join(runLedgerDir(repoDir, runID), filepath.FromSlash(record.Events)), stdout); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout, "logs:"); err != nil {
		return err
	}
	if err := copyTextFile(filepath.Join(runLedgerDir(repoDir, runID), filepath.FromSlash(record.Log)), stdout); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "evidence: %s\n", filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir)))
	return err
}

func printLedgerRunStatus(repoDir string, runID string, stdout io.Writer) error {
	return withRunLedgerRootLock(repoDir, false, func() error {
		return printLedgerRunStatusUnlocked(repoDir, runID, stdout)
	})
}

func printLedgerRunStatusUnlocked(repoDir string, runID string, stdout io.Writer) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "run: %s\nstate: %s\nscenario: %s\ntargetProfile: %s\nhost: %s\nstatus: %s\nacceptedAt: %s\nevidence: %s\n",
		record.RunID,
		record.State,
		record.Scenario,
		record.TargetProfile,
		record.Host,
		record.Status,
		record.AcceptedAt,
		filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir)),
	)
	return err
}

func updateRunLedgerAfterStop(repoDir string, runID string, stopErr error, now time.Time) error {
	if _, err := readRunLedgerRecord(repoDir, runID); err != nil {
		var usageErr *usageError
		if errors.As(err, &usageErr) {
			return nil
		}
		return err
	}
	return withRunLedgerLock(repoDir, runID, func() error {
		return updateRunLedgerAfterStopUnlocked(repoDir, runID, stopErr, now)
	})
}

func updateRunLedgerAfterStopUnlocked(repoDir string, runID string, stopErr error, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		var userErr *usageError
		if errors.As(err, &userErr) {
			return nil
		}
		return err
	}
	if record.State != "kept" && record.State != "cleanup-failed" {
		return nil
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if stopErr != nil {
		record.State = "cleanup-failed"
		record.CompletedAt = ""
	} else {
		if record.Status == resultStatusPassed {
			record.State = "completed"
		} else {
			record.State = "failed"
		}
		record.CompletedAt = record.UpdatedAt
		record.StopPhase = ""
	}
	if err := writeRunLedgerRecord(repoDir, record); err != nil {
		return err
	}
	stateEvents := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID, "events.jsonl")
	if _, err := os.Stat(stateEvents); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	policy, available := availableRunLedgerPolicy(repoDir)
	if !available {
		return nil
	}
	if syncErr := syncRunLedgerArtifacts(repoDir, &record, policy); syncErr != nil {
		return syncErr
	}
	return writeRunLedgerRecord(repoDir, record)
}

func checkpointRunLedgerStopPhase(repoDir string, runID string, phase string, now time.Time) error {
	return withRunLedgerLock(repoDir, runID, func() error {
		return checkpointRunLedgerStopPhaseUnlocked(repoDir, runID, phase, now)
	})
}

func checkpointRunLedgerStopPhaseUnlocked(repoDir string, runID string, phase string, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	record.StopPhase = phase
	if phase == "session-stopped" || phase == "broker-cleaned" || phase == "host-lock-released" {
		record.State = "cleanup-failed"
		record.CompletedAt = ""
		if record.Scenario == "" || record.TargetProfile == "" || record.Host == "" || record.Status == "" {
			bundle, err := readEvidenceBundleFile(filepath.Join(repoDir, filepath.FromSlash(record.EvidenceDir)))
			if err != nil {
				return err
			}
			if bundle.RunID != runID {
				return fmt.Errorf("evidence Bundle for %s identifies run %q", runID, bundle.RunID)
			}
			record.Scenario = bundle.Scenario
			record.TargetProfile = bundle.TargetProfile
			record.Host = bundle.Host
			record.Status = bundle.Status
		}
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	return writeRunLedgerRecord(repoDir, record)
}

func prepareRunLedgerForStop(repoDir string, runID string, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	ledgerErr := err
	ledgerCorrupt := false
	if err == nil {
		if record.State == "submitted" {
			return reconcileSubmittedRunLedgerForStop(repoDir, runID, now)
		}
		return refreshRunLedgerArtifacts(repoDir, runID, now)
	} else {
		var usageErr *usageError
		if !errors.As(err, &usageErr) {
			ledgerCorrupt = true
		}
	}
	manifest, stateDir, found, err := readStopRunManifest(repoDir, runID)
	if err != nil {
		return err
	}
	if !found {
		if ledgerCorrupt {
			return ledgerErr
		}
		return nil
	}
	retentionRecord, err := readRunRetentionRecord(repoDir, stateDir, manifest)
	if err != nil {
		return err
	}
	if retentionRecord.LegacyMissingRecord && manifest.Runtime.BrokerAdapter != "fake" {
		if ledgerCorrupt {
			return ledgerErr
		}
		return nil
	}
	acceptedAt, err := acceptedAtFromRunID(runID)
	if err != nil {
		return err
	}
	if ledgerCorrupt {
		if err := cleanupRunLedgerRecord(repoDir, runID); err != nil {
			return fmt.Errorf("remove corrupt embedded run ledger for %s: %w", runID, err)
		}
	}
	if err := initializeRunLedger(repoDir, manifest, acceptedAt, filepath.Join(stateDir, "events.jsonl")); err != nil {
		return fmt.Errorf("reconstruct embedded run ledger for %s: %w", runID, err)
	}
	record, err = readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	if err := reconcileSubmittedRunLedgerForStop(repoDir, runID, now); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cleanupRunLedgerRecord(repoDir, runID)
		}
		return err
	}
	return nil
}

func reconcileSubmittedRunLedgerForStop(repoDir string, runID string, now time.Time) error {
	return withRunLedgerLock(repoDir, runID, func() error {
		return reconcileSubmittedRunLedgerForStopUnlocked(repoDir, runID, now)
	})
}

func reconcileSubmittedRunLedgerForStopUnlocked(repoDir string, runID string, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	if record.State != "submitted" {
		return refreshRunLedgerArtifactsUnlocked(repoDir, runID, now)
	}
	manifest, stateDir, found, err := readStopRunManifest(repoDir, runID)
	if err != nil {
		return err
	}
	if !found {
		return newUsageError("run %q not found", runID)
	}
	retentionRecord, err := readRunRetentionRecord(repoDir, stateDir, manifest)
	if err != nil {
		return err
	}
	status := resultStatusFailed
	bundle, err := readEvidenceBundleFile(filepath.Join(repoDir, "artifacts", "maya-stall", runID))
	if err == nil {
		status = bundle.Status
	} else if !errors.Is(err, os.ErrNotExist) || retentionRecord.Status != "running" {
		return err
	}
	record.Scenario = manifest.Scenario
	record.TargetProfile = manifest.TargetProfile
	record.Host = manifest.Host
	record.State = "kept"
	if retentionRecord.Status == "running" || retentionRecord.StopPhase != "" {
		record.State = "cleanup-failed"
	}
	if retentionRecord.Status == "running" && retentionRecord.StopPhase == "" {
		active, err := repoHostLockUsesActiveRun(repoDir, manifest.Host, runID)
		if err != nil {
			return err
		}
		if active {
			if err := requireStaleSubmittedRunController(repoDir, manifest.Host, runID); err != nil {
				return err
			}
		}
	}
	record.Status = status
	record.StopPhase = retentionRecord.StopPhase
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	if err := writeRunLedgerRecord(repoDir, record); err != nil {
		return err
	}
	return refreshRunLedgerArtifactsUnlocked(repoDir, runID, now)
}

func repoHostLockUsesActiveRun(repoDir string, hostID string, runID string) (bool, error) {
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", hostID+".lock")
	info, err := os.Lstat(lockPath)
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("host lock %s must be a regular file, not a symlink", lockPath)
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		return false, err
	}
	owner := parseHostLockOwner(string(content))
	if owner.ActiveRun == runID {
		return true, nil
	}
	if owner.KeptRun == runID {
		return false, nil
	}
	return false, fmt.Errorf("host lock for %s is not owned by run %s", hostID, runID)
}

func requireStaleSubmittedRunController(repoDir string, hostID string, runID string) error {
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", hostID+".lock")
	info, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("host lock %s must be a regular file, not a symlink", lockPath)
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	owner := parseHostLockOwner(string(content))
	if owner.ActiveRun != runID {
		return fmt.Errorf("host lock for %s is not owned by active run %s", hostID, runID)
	}
	stale, err := isStaleHostLock(lockPath)
	if err != nil {
		return err
	}
	if !stale {
		return fmt.Errorf("run %s still has a live controller; refusing crash-recovery stop", runID)
	}
	return nil
}

func acceptedAtFromRunID(runID string) (time.Time, error) {
	base := runID
	if separator := strings.IndexByte(base, '-'); separator >= 0 {
		base = base[:separator]
	}
	acceptedAt, err := time.Parse("20060102T150405.000000000Z", base)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse accepted time from Run ID %s: %w", runID, err)
	}
	return acceptedAt, nil
}

func refreshRunLedgerArtifacts(repoDir string, runID string, now time.Time) error {
	return withRunLedgerLock(repoDir, runID, func() error {
		return refreshRunLedgerArtifactsUnlocked(repoDir, runID, now)
	})
}

func refreshRunLedgerArtifactsUnlocked(repoDir string, runID string, now time.Time) error {
	record, err := readRunLedgerRecord(repoDir, runID)
	if err != nil {
		return err
	}
	policy := fallbackRunLedgerPolicy(record)
	if configuredPolicy, available := availableRunLedgerPolicy(repoDir); available {
		policy = configuredPolicy
	}
	if err := syncRunLedgerArtifacts(repoDir, &record, policy); err != nil {
		return err
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	return writeRunLedgerRecord(repoDir, record)
}

func fallbackRunLedgerPolicy(record runLedgerRecord) runLedgerPolicy {
	policy := defaultRunLedgerPolicy()
	if record.EventsTruncated && record.EventCount >= 3 {
		policy.MaxEvents = record.EventCount
	}
	if record.EventsTruncated && record.EventBytes >= 1024 {
		policy.MaxEventBytes = record.EventBytes
	}
	if record.LogTruncated && record.LogBytes >= 96 {
		policy.MaxLogBytes = record.LogBytes
	}
	return policy
}
