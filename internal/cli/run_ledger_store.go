package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// runLedgerStore is the persistence seam for durable Run records and their
// bounded event and log artifacts. Callers deal in Run identity and snapshots;
// filesystem layout and locking stay inside the module.
type runLedgerStore struct {
	repoDir string
}

type runLedgerSnapshot struct {
	Record runLedgerRecord
	Events []byte
	Log    []byte
}

func (store runLedgerStore) validateRunDir(runID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	return ensureOutputPathHasNoSymlinkParent(store.repoDir, filepath.Join(".maya-stall", "state", "ledger", "runs", runID))
}

func (store runLedgerStore) artifactPath(record runLedgerRecord, relativePath string) (string, error) {
	ledgerDir := runLedgerDir(store.repoDir, record.RunID)
	if err := ensureWorkspacePathHasNoSymlinkAncestor(ledgerDir, filepath.FromSlash(relativePath)); err != nil {
		return "", err
	}
	return filepath.Join(ledgerDir, filepath.FromSlash(relativePath)), nil
}

func (store runLedgerStore) readArtifact(runID string, selectPath func(runLedgerRecord) string) (runLedgerRecord, []byte, error) {
	var record runLedgerRecord
	var content []byte
	err := withRunLedgerLock(store.repoDir, runID, func() error {
		var err error
		record, err = store.Read(runID)
		if err != nil {
			return err
		}
		path, err := store.artifactPath(record, selectPath(record))
		if err != nil {
			return err
		}
		content, err = readRunLedgerBytes(path)
		return err
	})
	return record, content, err
}

func (store runLedgerStore) ReadEvents(runID string) (runLedgerRecord, []byte, error) {
	return store.readArtifact(runID, func(record runLedgerRecord) string { return record.Events })
}

func (store runLedgerStore) ReadLog(runID string) (runLedgerRecord, []byte, error) {
	return store.readArtifact(runID, func(record runLedgerRecord) string { return record.Log })
}

func newRunLedgerStore(repoDir string) runLedgerStore {
	return runLedgerStore{repoDir: repoDir}
}

func (store runLedgerStore) Read(runID string) (runLedgerRecord, error) {
	return readRunLedgerRecord(store.repoDir, runID)
}

func (store runLedgerStore) HasRecord(runID string) (bool, error) {
	if err := store.validateRunDir(runID); err != nil {
		return false, err
	}
	_, err := os.Lstat(filepath.Join(runLedgerDir(store.repoDir, runID), "run.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Occupied reports whether durable state has claimed the Run ID, including an
// interrupted initialization that has not published run.json yet.
func (store runLedgerStore) Occupied(runID string) (bool, error) {
	if err := store.validateRunDir(runID); err != nil {
		return false, err
	}
	_, err := os.Lstat(runLedgerDir(store.repoDir, runID))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (store runLedgerStore) Update(runID string, mutate func(*runLedgerRecord) error) error {
	return withRunLedgerLock(store.repoDir, runID, func() error {
		record, err := store.Read(runID)
		if err != nil {
			return err
		}
		if err := mutate(&record); err != nil {
			return err
		}
		return store.Replace(record)
	})
}

func (store runLedgerStore) UpdateWithArtifacts(runID string, mutate func(*runLedgerRecord) error) error {
	return store.Update(runID, func(record *runLedgerRecord) error {
		if err := syncRunLedgerArtifacts(store.repoDir, record, fallbackRunLedgerPolicy(*record)); err != nil {
			return err
		}
		return mutate(record)
	})
}

// Replace participates in a wider durable transaction whose journal owns
// recovery. Ordinary callers should use Update so the Run lock is held.
func (store runLedgerStore) Replace(record runLedgerRecord) error {
	return writeRunLedgerRecord(store.repoDir, record)
}

func (store runLedgerStore) Snapshot(runID string) (runLedgerSnapshot, error) {
	var snapshot runLedgerSnapshot
	err := withRunLedgerLock(store.repoDir, runID, func() error {
		record, err := store.Read(runID)
		if err != nil {
			return err
		}
		eventsPath, err := store.artifactPath(record, record.Events)
		if err != nil {
			return err
		}
		logPath, err := store.artifactPath(record, record.Log)
		if err != nil {
			return err
		}
		events, err := readRunLedgerBytes(eventsPath)
		if err != nil {
			return err
		}
		logContent, err := readRunLedgerBytes(logPath)
		if err != nil {
			return err
		}
		snapshot = runLedgerSnapshot{Record: record, Events: events, Log: logContent}
		return nil
	})
	return snapshot, err
}

func (store runLedgerStore) UpdateSnapshot(runID string, mutate func(*runLedgerSnapshot) error) error {
	return withRunLedgerLock(store.repoDir, runID, func() error {
		record, err := store.Read(runID)
		if err != nil {
			return err
		}
		eventsPath, err := store.artifactPath(record, record.Events)
		if err != nil {
			return err
		}
		logPath, err := store.artifactPath(record, record.Log)
		if err != nil {
			return err
		}
		events, err := readRunLedgerBytes(eventsPath)
		if err != nil {
			return err
		}
		logContent, err := readRunLedgerBytes(logPath)
		if err != nil {
			return err
		}
		snapshot := runLedgerSnapshot{Record: record, Events: events, Log: logContent}
		if err := mutate(&snapshot); err != nil {
			return err
		}
		if !bytes.Equal(events, snapshot.Events) {
			if err := writeRunLedgerBytes(eventsPath, snapshot.Events); err != nil {
				return err
			}
		}
		if !bytes.Equal(logContent, snapshot.Log) {
			if err := writeRunLedgerBytes(logPath, snapshot.Log); err != nil {
				return err
			}
		}
		return store.Replace(snapshot.Record)
	})
}

func (store runLedgerStore) Refresh(runID string, now time.Time) error {
	return refreshRunLedgerArtifacts(store.repoDir, runID, now)
}

func (store runLedgerStore) SyncArtifacts(record *runLedgerRecord, policy runLedgerPolicy) error {
	return syncRunLedgerArtifacts(store.repoDir, record, policy)
}

func (store runLedgerStore) ReplaceEvents(runID string, content []byte) error {
	return withRunLedgerLock(store.repoDir, runID, func() error {
		record, err := store.Read(runID)
		if err != nil {
			return err
		}
		path, err := store.artifactPath(record, record.Events)
		if err != nil {
			return err
		}
		return writeRunLedgerBytes(path, content)
	})
}

func (store runLedgerStore) Initialize(manifest runManifest, acceptedAt time.Time, sourceEventsPath string) error {
	return initializeRunLedger(store.repoDir, manifest, acceptedAt, sourceEventsPath)
}

func (store runLedgerStore) Finalize(outcome runOutcome, manifest runManifest, policy runLedgerPolicy, now time.Time) error {
	return finalizeRunLedger(store.repoDir, outcome, manifest, policy, now)
}

func (store runLedgerStore) Remove(runID string) error {
	return cleanupRunLedgerRecord(store.repoDir, runID)
}

func (store runLedgerStore) Prune(policy runLedgerPolicy, now time.Time, currentRunID string) error {
	return pruneRunLedger(store.repoDir, policy, now, currentRunID)
}

func (store runLedgerStore) PreserveAcknowledgedFailure(runID string, acknowledged []byte, terminal []byte, acknowledgedLog []byte, diagnostic string, policy runLedgerPolicy) error {
	return withRunLedgerLock(store.repoDir, runID, func() error {
		return store.preserveAcknowledgedFailure(runID, acknowledged, terminal, acknowledgedLog, diagnostic, policy)
	})
}

func (store runLedgerStore) FinalizeAcknowledgedFailure(outcome runOutcome, manifest runManifest, policy runLedgerPolicy, now time.Time, acknowledged []byte, terminal []byte, acknowledgedLog []byte, diagnostic string, restore *runLedgerRecord) (runLedgerRecord, error) {
	var terminalLedger runLedgerRecord
	err := withRunLedgerLock(store.repoDir, manifest.RunID, func() error {
		ledgerErr := finalizeRunLedgerUnlocked(store.repoDir, outcome, manifest, policy, now)
		var preserveErr error
		if ledgerErr == nil {
			preserveErr = store.preserveAcknowledgedFailure(manifest.RunID, acknowledged, terminal, acknowledgedLog, diagnostic, policy)
		}
		var terminalErr error
		if ledgerErr == nil && preserveErr == nil {
			terminalLedger, terminalErr = store.Read(manifest.RunID)
		}
		var restoreErr error
		if restore != nil {
			restoreErr = store.Replace(*restore)
		}
		return errors.Join(ledgerErr, preserveErr, terminalErr, restoreErr)
	})
	return terminalLedger, err
}

func (store runLedgerStore) preserveAcknowledgedFailure(runID string, acknowledged []byte, terminal []byte, acknowledgedLog []byte, diagnostic string, policy runLedgerPolicy) error {
	record, err := store.Read(runID)
	if err != nil {
		return err
	}
	lines := bytes.Split(bytes.TrimSpace(acknowledged), []byte{'\n'})
	maxSequence := 0
	hasFailure := false
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		sequence := ledgerEventSequence(event)
		if event["type"] == "run-ledger.events.truncated" {
			gapLast, ok := validHostAgentEventGap(line, sequence)
			if !ok {
				return fmt.Errorf("invalid acknowledged Host Agent event gap")
			}
			sequence = gapLast
		}
		if sequence > maxSequence {
			maxSequence = sequence
		}
		hasFailure = hasFailure || fmt.Sprint(event["type"]) == "run.failed"
	}
	if !hasFailure {
		var failureEvent map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(terminal), []byte{'\n'}) {
			var event map[string]any
			if json.Unmarshal(line, &event) == nil && fmt.Sprint(event["type"]) == "run.failed" {
				failureEvent = event
				break
			}
		}
		if failureEvent == nil {
			failureEvent = map[string]any{"event": "run.failed", "type": "run.failed", "timestamp": record.UpdatedAt, "details": map[string]any{"message": diagnostic}}
		}
		failureEvent = normalizeRunLedgerEvent(failureEvent, maxSequence+1, record.UpdatedAt)
		encoded, err := json.Marshal(failureEvent)
		if err != nil {
			return err
		}
		lines = append(lines, encoded)
	}
	temporaryDir, err := os.MkdirTemp(store.repoDir, ".host-agent-failure-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temporaryDir) }()
	eventsSource := filepath.Join(temporaryDir, runLedgerEventsFileName)
	if err := writeRunLedgerBytes(eventsSource, append(bytes.Join(lines, []byte{'\n'}), '\n')); err != nil {
		return err
	}
	eventsPath := filepath.Join(runLedgerDir(store.repoDir, runID), filepath.FromSlash(record.Events))
	record.EventCount, record.EventsOmitted, record.EventsTruncated, record.EventBytes, err = copyBoundedLedgerEvents(eventsSource, eventsPath, policy.MaxEvents, policy.MaxEventBytes, record.AcceptedAt)
	if err != nil {
		return err
	}
	combinedLog := append([]byte(nil), acknowledgedLog...)
	if len(combinedLog) > 0 && combinedLog[len(combinedLog)-1] != '\n' {
		combinedLog = append(combinedLog, '\n')
	}
	combinedLog = append(combinedLog, []byte("Host Agent failure: "+diagnostic+"\n")...)
	logSource := filepath.Join(temporaryDir, "failure.log")
	if err := writeRunLedgerBytes(logSource, combinedLog); err != nil {
		return err
	}
	record.LogBytes, record.LogTruncated, err = copyBoundedLedgerLog(logSource, filepath.Join(runLedgerDir(store.repoDir, runID), filepath.FromSlash(record.Log)), policy.MaxLogBytes)
	if err != nil {
		return err
	}
	return store.Replace(record)
}
