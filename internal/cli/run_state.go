package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type statusOptions struct {
	RunID string
}

type keptRun struct {
	RunID        string
	StateDir     string
	Manifest     runManifest
	Record       runRetentionRecord
	RemoteStatus retainedRunStatus
	Bundle       evidenceBundle
}

func parseStatusArgs(args []string) (statusOptions, error) {
	var options statusOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run":
			i++
			if i >= len(args) || args[i] == "" {
				return statusOptions{}, newUsageError("--run needs a run id")
			}
			if err := validateRunID(args[i]); err != nil {
				return statusOptions{}, err
			}
			options.RunID = args[i]
		default:
			return statusOptions{}, newUsageError("unknown status option %q", args[i])
		}
	}
	return options, nil
}

func parseRunIDArg(command string, args []string) (string, error) {
	if len(args) != 1 {
		return "", newUsageError("%s needs one run id", command)
	}
	if err := validateRunID(args[0]); err != nil {
		return "", err
	}
	return args[0], nil
}

func validateRunID(runID string) error {
	if runID == "" {
		return newUsageError("run id must not be empty")
	}
	if runID == "." || runID == ".." || filepath.Clean(runID) != runID {
		return newUsageError("run id %q must name one run directory", runID)
	}
	if !hostIDPattern.MatchString(runID) {
		return newUsageError("run id %q must contain only letters, numbers, dots, underscores, or dashes", runID)
	}
	return nil
}

func printStatus(repoDir string, options statusOptions, stdout io.Writer) error {
	if options.RunID != "" {
		run, err := readKeptRun(repoDir, options.RunID)
		if err != nil {
			return err
		}
		if err := refreshKeptRunStatus(&run); err != nil {
			return err
		}
		printKeptRunStatus(stdout, run)
		return nil
	}
	runs, err := listKeptRuns(repoDir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "state: no kept sessions")
		return nil
	}
	for _, run := range runs {
		printKeptRunStatus(stdout, run)
	}
	return nil
}

func printKeptRunStatus(stdout io.Writer, run keptRun) {
	fmt.Fprintf(stdout, "run: %s\n", run.RunID)
	state := run.RemoteStatus.State
	if state == "" {
		state = "kept"
	}
	fmt.Fprintf(stdout, "state: %s\n", state)
	fmt.Fprintf(stdout, "scenario: %s\n", run.Manifest.Scenario)
	fmt.Fprintf(stdout, "targetProfile: %s\n", run.Manifest.TargetProfile)
	fmt.Fprintf(stdout, "host: %s\n", run.Manifest.Host)
	if run.Manifest.Runtime.Profile != "" {
		fmt.Fprintf(stdout, "runtime: %s\n", run.Manifest.Runtime.Profile)
		fmt.Fprintf(stdout, "hostAdapter: %s\n", run.Manifest.Runtime.HostAdapter)
		fmt.Fprintf(stdout, "brokerAdapter: %s\n", run.Manifest.Runtime.BrokerAdapter)
		fmt.Fprintf(stdout, "liveProofEligible: %t\n", run.Manifest.Runtime.LiveProofEligible)
	}
	fmt.Fprintf(stdout, "status: %s\n", run.Bundle.Status)
	if run.Record.RetentionReason != "" {
		fmt.Fprintf(stdout, "retentionReason: %s\n", run.Record.RetentionReason)
	}
	if run.RemoteStatus.BrokerStatus != "" {
		fmt.Fprintf(stdout, "remoteState: %s\n", run.RemoteStatus.BrokerStatus)
	}
	if run.RemoteStatus.SessionID != "" {
		fmt.Fprintf(stdout, "brokerSession: %s\n", run.RemoteStatus.SessionID)
	}
	if run.RemoteStatus.RemoteWorkspace != "" {
		fmt.Fprintf(stdout, "remoteWorkspace: %s\n", run.RemoteStatus.RemoteWorkspace)
	}
	if run.RemoteStatus.Detail != "" {
		fmt.Fprintf(stdout, "detail: %s\n", run.RemoteStatus.Detail)
	}
	fmt.Fprintf(stdout, "stateDir: %s\n", run.StateDir)
}

func attachRun(repoDir string, runID string, stdout io.Writer) error {
	run, err := readKeptRunState(repoDir, runID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "run: %s\n", run.RunID)
	if err := attachLocalRunFiles(run, stdout); err != nil {
		return err
	}
	broker, err := retentionBrokerForRecord(run.Record)
	if err != nil {
		return err
	}
	capabilities := broker.RetentionCapabilities()
	if err := requireRetentionCapability(broker, run.Manifest.Runtime.BrokerAdapter, "attach/log observation", capabilities.AttachLogObservation); err != nil {
		return err
	}
	return broker.AttachRetainedRun(run.Record, stdout)
}

func stopRun(repoDir string, runID string) error {
	manifest, stateDir, found, err := readStopRunManifest(repoDir, runID)
	if err != nil {
		return err
	}
	if !found {
		return releaseMatchingKeptHostLock(repoDir, runID)
	}
	if err := validateHostID(manifest.Host); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", manifest.Host+".lock")
	keptRun, found, err := readHostLockKeptRun(lockPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Host Lock for %s is not a kept run lock", manifest.Host)
	}
	if found && keptRun != runID {
		return fmt.Errorf("Host Lock for %s belongs to kept run %s, not %s", manifest.Host, keptRun, runID)
	}
	record, err := readRunRetentionRecord(repoDir, stateDir, manifest)
	if err != nil {
		return err
	}
	if record.LegacyMissingRecord && manifest.Runtime.BrokerAdapter != "fake" {
		if err := cleanupRunState(repoDir, runID); err != nil {
			return err
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	broker, err := retentionBrokerForRecord(record)
	if err != nil {
		return err
	}
	capabilities := broker.RetentionCapabilities()
	if err := requireRetentionCapability(broker, manifest.Runtime.BrokerAdapter, "stop retained session", capabilities.StopRetainedSession); err != nil {
		return err
	}
	if err := broker.StopRetainedRun(record); err != nil {
		return err
	}
	if err := cleanupRunState(repoDir, runID); err != nil {
		return err
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readStopRunManifest(repoDir string, runID string) (runManifest, string, bool, error) {
	if err := validateRunID(runID); err != nil {
		return runManifest{}, "", false, err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "runs", runID)); err != nil {
		return runManifest{}, "", false, err
	}
	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	manifestBytes, err := os.ReadFile(filepath.Join(stateDir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return runManifest{}, stateDir, false, nil
	}
	if err != nil {
		return runManifest{}, "", false, err
	}
	var manifest runManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return runManifest{}, "", false, fmt.Errorf("parse kept run manifest: %w", err)
	}
	return manifest, stateDir, true, nil
}

func releaseMatchingKeptHostLock(repoDir string, runID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	lockDir := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts")
	entries, err := os.ReadDir(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		return newUsageError("kept run %q not found", runID)
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}
		lockPath := filepath.Join(lockDir, entry.Name())
		keptRun, found, err := readHostLockKeptRun(lockPath)
		if err != nil {
			return err
		}
		if found && keptRun == runID {
			return os.Remove(lockPath)
		}
	}
	return newUsageError("kept run %q not found", runID)
}

func cleanupRunState(repoDir string, runID string) error {
	if err := validateRunID(runID); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "runs", runID)); err != nil {
		return err
	}
	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	return os.RemoveAll(stateDir)
}

func listKeptRuns(repoDir string) ([]keptRun, error) {
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return nil, err
	}
	lockDir := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts")
	entries, err := os.ReadDir(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]keptRun, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".lock" {
			continue
		}
		runID, found, err := readHostLockKeptRun(filepath.Join(lockDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		run, err := readKeptRun(repoDir, runID)
		if err != nil {
			return nil, err
		}
		if err := refreshKeptRunStatus(&run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func readKeptRun(repoDir string, runID string) (keptRun, error) {
	run, err := readKeptRunState(repoDir, runID)
	if err != nil {
		return keptRun{}, err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join("artifacts", "maya-stall", runID)); err != nil {
		return keptRun{}, err
	}
	evidenceBytes, err := os.ReadFile(filepath.Join(repoDir, "artifacts", "maya-stall", runID, "evidence.json"))
	if err != nil {
		return keptRun{}, err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(evidenceBytes, &bundle); err != nil {
		return keptRun{}, fmt.Errorf("parse kept run evidence: %w", err)
	}
	run.Bundle = bundle
	return run, nil
}

func readKeptRunState(repoDir string, runID string) (keptRun, error) {
	manifest, stateDir, err := readKeptRunManifest(repoDir, runID)
	if err != nil {
		return keptRun{}, err
	}
	if err := ensureRunHasKeptLock(repoDir, manifest, runID); err != nil {
		return keptRun{}, err
	}
	record, err := readRunRetentionRecord(repoDir, stateDir, manifest)
	if err != nil {
		return keptRun{}, err
	}
	return keptRun{RunID: runID, StateDir: stateDir, Manifest: manifest, Record: record}, nil
}

func readRunRetentionRecord(repoDir string, stateDir string, manifest runManifest) (runRetentionRecord, error) {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(stateDir, "run-record.json"); err != nil {
		return runRetentionRecord{}, err
	}
	content, err := os.ReadFile(filepath.Join(stateDir, "run-record.json"))
	if errors.Is(err, os.ErrNotExist) {
		return fallbackRunRetentionRecord(repoDir, stateDir, manifest), nil
	}
	if err != nil {
		return runRetentionRecord{}, err
	}
	var record runRetentionRecord
	if err := json.Unmarshal(content, &record); err != nil {
		return runRetentionRecord{}, fmt.Errorf("parse kept run record: %w", err)
	}
	return record, nil
}

func refreshKeptRunStatus(run *keptRun) error {
	if run.Record.LegacyMissingRecord && run.Manifest.Runtime.BrokerAdapter != "fake" {
		run.RemoteStatus = retainedRunStatus{
			State:           "stale",
			BrokerStatus:    "unknown",
			RemoteWorkspace: run.Record.RemoteWorkspace,
			Detail:          "missing Run Record; broker-backed status is unavailable for this legacy kept run",
		}
		return nil
	}
	broker, err := retentionBrokerForRecord(run.Record)
	if err != nil {
		return err
	}
	capabilities := broker.RetentionCapabilities()
	if err := requireRetentionCapability(broker, run.Manifest.Runtime.BrokerAdapter, "status retained session", capabilities.StatusRetainedSession); err != nil {
		return err
	}
	status, err := broker.StatusRetainedRun(run.Record)
	if err != nil {
		return err
	}
	run.RemoteStatus = status
	return nil
}

func readKeptRunManifest(repoDir string, runID string) (runManifest, string, error) {
	if err := validateRunID(runID); err != nil {
		return runManifest{}, "", err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "runs", runID)); err != nil {
		return runManifest{}, "", err
	}
	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	manifestBytes, err := os.ReadFile(filepath.Join(stateDir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return runManifest{}, "", newUsageError("kept run %q not found", runID)
	}
	if err != nil {
		return runManifest{}, "", err
	}
	var manifest runManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return runManifest{}, "", fmt.Errorf("parse kept run manifest: %w", err)
	}
	return manifest, stateDir, nil
}

func ensureRunHasKeptLock(repoDir string, manifest runManifest, runID string) error {
	if err := validateHostID(manifest.Host); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", manifest.Host+".lock")
	keptRun, found, err := readHostLockKeptRun(lockPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Host Lock for %s is not a kept run lock", manifest.Host)
	}
	if keptRun != runID {
		return fmt.Errorf("Host Lock for %s belongs to kept run %s, not %s", manifest.Host, keptRun, runID)
	}
	return nil
}

func readHostLockKeptRun(lockPath string) (string, bool, error) {
	info, err := os.Lstat(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("Host Lock %s must not be a symlink", lockPath)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("Host Lock %s must be a regular file", lockPath)
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(key) == "keptRun" {
			return strings.TrimSpace(value), strings.TrimSpace(value) != "", nil
		}
	}
	return "", false, nil
}

func copyRunStateTextFile(stateDir string, relativePath string, stdout io.Writer) error {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(stateDir, relativePath); err != nil {
		return err
	}
	return copyTextFile(filepath.Join(stateDir, relativePath), stdout)
}

func copyTextFile(path string, stdout io.Writer) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(stdout, file)
	return err
}
