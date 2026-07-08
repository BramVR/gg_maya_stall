package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const runScopedScreenshotName = "run-scoped-screenshot.png"
const runScopedVisualEvidenceStateName = "run-scoped-visual-evidence.json"

type attachOptions struct {
	RunID  string
	Action string
	X      int
	Y      int
}

func parseAttachArgs(args []string) (attachOptions, error) {
	if len(args) == 0 {
		return attachOptions{}, newUsageError("attach needs one run id")
	}
	if err := validateRunID(args[0]); err != nil {
		return attachOptions{}, err
	}
	options := attachOptions{RunID: args[0], X: -1, Y: -1}
	if len(args) == 1 {
		options.Action = "observe"
		return options, nil
	}
	switch args[1] {
	case "screenshot":
		if len(args) != 2 {
			return attachOptions{}, newUsageError("attach screenshot does not accept options")
		}
		options.Action = "screenshot"
		return options, nil
	case "control":
		return parseAttachControlArgs(options, args[2:])
	default:
		return attachOptions{}, newUsageError("unknown attach action %q", args[1])
	}
}

func parseAttachControlArgs(options attachOptions, args []string) (attachOptions, error) {
	if len(args) == 0 {
		return attachOptions{}, newUsageError("expected attach control action click")
	}
	if args[0] != "click" {
		return attachOptions{}, newUsageError("unknown attach control action %q", args[0])
	}
	options.Action = "control-click"
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--x":
			i++
			if i >= len(args) || args[i] == "" {
				return attachOptions{}, newUsageError("--x needs a coordinate")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return attachOptions{}, newUsageError("invalid --x coordinate %q", args[i])
			}
			options.X = value
		case "--y":
			i++
			if i >= len(args) || args[i] == "" {
				return attachOptions{}, newUsageError("--y needs a coordinate")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return attachOptions{}, newUsageError("invalid --y coordinate %q", args[i])
			}
			options.Y = value
		default:
			return attachOptions{}, newUsageError("unknown attach control option %q", args[i])
		}
	}
	if options.X < 0 || options.Y < 0 {
		return attachOptions{}, newUsageError("desktop click coordinates must be non-negative")
	}
	return options, nil
}

func runAttachAction(repoDir string, options attachOptions, stdout io.Writer) error {
	switch options.Action {
	case "observe":
		return attachRun(repoDir, options.RunID, stdout)
	case "screenshot":
		outcome, artifact, err := captureRunScopedScreenshot(repoDir, options.RunID)
		if err != nil {
			return err
		}
		printVisualEvidenceOutcome(stdout, outcome, artifact)
		return nil
	case "control-click":
		outcome, err := clickRunScopedDesktop(repoDir, options)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "run: %s\n", options.RunID)
		printDesktopControlOutcome(stdout, outcome)
		return nil
	default:
		return newUsageError("unknown attach action %q", options.Action)
	}
}

func captureRunScopedScreenshot(repoDir string, runID string) (runOutcome, visualEvidenceArtifact, error) {
	run, context, broker, err := runScopedOperationContext(repoDir, runID)
	if err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	capturer, ok := broker.(screenshotCapturer)
	if !ok {
		return runOutcome{}, visualEvidenceArtifact{}, fmt.Errorf("session broker does not support screenshot capture")
	}
	if err := rejectRunScopedStateWriteLeaves(context, true); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	artifact, err := capturer.CaptureScreenshot(context, screenshotRequest{Name: runScopedScreenshotName})
	if err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	artifact.TargetProfile = run.Manifest.TargetProfile
	artifact.Host = run.Manifest.Host
	if err := appendEvent(context.EventsPath, "attach.screenshot.captured", artifact.Path); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if err := appendRunScopedVisualEvidence(repoDir, runID, artifact); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	return runScopedOutcome(run, context), artifact, nil
}

func clickRunScopedDesktop(repoDir string, options attachOptions) (desktopControlOutcome, error) {
	run, context, broker, err := runScopedOperationContext(repoDir, options.RunID)
	if err != nil {
		return desktopControlOutcome{}, err
	}
	clicker, ok := broker.(desktopClicker)
	if !ok {
		return desktopControlOutcome{}, fmt.Errorf("session broker does not support desktop control")
	}
	if err := rejectRunScopedStateWriteLeaves(context, false); err != nil {
		return desktopControlOutcome{}, err
	}
	remoteRoot := remoteJoin(context.RunWorkspace.RemoteRunRoot(), "desktop-control", "attach-click")
	if _, ok := broker.(ggMayaSessiondBroker); ok {
		remoteRunRoot, err := retainedRemoteRunRoot(run.Record)
		if err != nil {
			return desktopControlOutcome{}, err
		}
		remoteRoot = remoteJoin(remoteRunRoot, "desktop-control", "attach-click")
	} else if run.Record.RemoteRunRoot != "" {
		remoteRoot = remoteJoin(run.Record.RemoteRunRoot, "desktop-control", "attach-click")
	}
	if err := clicker.ClickDesktop(desktopClickRequest{RemoteRoot: remoteRoot, X: options.X, Y: options.Y}); err != nil {
		return desktopControlOutcome{}, err
	}
	if err := appendEvent(context.EventsPath, "attach.control.click", fmt.Sprintf("x=%d y=%d", options.X, options.Y)); err != nil {
		return desktopControlOutcome{}, err
	}
	return desktopControlOutcome{
		Action:        "click",
		TargetProfile: run.Manifest.TargetProfile,
		Host:          run.Manifest.Host,
		Runtime:       run.Manifest.Runtime,
		X:             options.X,
		Y:             options.Y,
		DryRun:        false,
	}, nil
}

func runScopedOperationContext(repoDir string, runID string) (keptRun, runContext, any, error) {
	run, err := readRunScopedState(repoDir, runID)
	if err != nil {
		return keptRun{}, runContext{}, nil, err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join("artifacts", "maya-stall", runID)); err != nil {
		return keptRun{}, runContext{}, nil, err
	}
	workspace, err := newRunWorkspace(repoDir, runID, run.Record.HostConfig.WorkRoot, evidenceStandaloneResultName)
	if err != nil {
		return keptRun{}, runContext{}, nil, err
	}
	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	evidenceDir := filepath.Join(repoDir, "artifacts", "maya-stall", runID)
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return keptRun{}, runContext{}, nil, err
	}
	broker, err := retentionBrokerForRecord(run.Record)
	if err != nil {
		return keptRun{}, runContext{}, nil, err
	}
	context := runContext{
		RepoDir:            repoDir,
		RunWorkspace:       workspace,
		StateDir:           stateDir,
		EvidenceDir:        evidenceDir,
		Workspace:          filepath.Join(stateDir, "workspace"),
		EventsPath:         filepath.Join(stateDir, "events.jsonl"),
		LogPath:            filepath.Join(stateDir, "logs", "session.log"),
		ScenarioResultPath: run.Record.ScenarioResultPath,
	}
	return run, context, broker, nil
}

func rejectRunScopedStateWriteLeaves(context runContext, includeVisualEvidenceState bool) error {
	for _, path := range []string{context.EventsPath} {
		if err := rejectExistingFileLeaf(path); err != nil {
			return err
		}
	}
	if includeVisualEvidenceState {
		if err := rejectExistingFileLeaf(filepath.Join(context.StateDir, runScopedVisualEvidenceStateName)); err != nil {
			return err
		}
	}
	return nil
}

func readRunScopedState(repoDir string, runID string) (keptRun, error) {
	manifest, stateDir, err := readKeptRunManifest(repoDir, runID)
	if err != nil {
		return keptRun{}, err
	}
	if err := ensureRunHasScopedHostLock(repoDir, manifest, runID); err != nil {
		return keptRun{}, err
	}
	record, err := readRunRetentionRecord(repoDir, stateDir, manifest)
	if err != nil {
		return keptRun{}, err
	}
	return keptRun{RunID: runID, StateDir: stateDir, Manifest: manifest, Record: record}, nil
}

func runScopedOutcome(run keptRun, context runContext) runOutcome {
	status := run.Record.Status
	if status == "" {
		status = "kept"
	}
	return runOutcome{
		RunID:         run.RunID,
		Scenario:      run.Manifest.Scenario,
		TargetProfile: run.Manifest.TargetProfile,
		Host:          run.Manifest.Host,
		StateDir:      context.StateDir,
		EvidenceDir:   context.EvidenceDir,
		Result:        ScenarioResult{Status: status},
		StopPolicy:    status,
	}
}

func appendRunScopedVisualEvidence(repoDir string, runID string, artifact visualEvidenceArtifact) error {
	statePath := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID, runScopedVisualEvidenceStateName)
	if err := appendRunScopedVisualEvidenceState(statePath, artifact); err != nil {
		return err
	}
	path := filepath.Join(repoDir, "artifacts", "maya-stall", runID, evidenceBundleFileName)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return fmt.Errorf("parse Evidence Bundle for run-scoped screenshot: %w", err)
	}
	for index, existing := range bundle.VisualEvidence {
		if existing.Path == artifact.Path && existing.Kind == artifact.Kind {
			bundle.VisualEvidence[index] = artifact
			bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
			return writeJSONFile(path, bundle)
		}
	}
	bundle.VisualEvidence = append(bundle.VisualEvidence, artifact)
	bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
	return writeJSONFile(path, bundle)
}

func appendRunScopedVisualEvidenceState(path string, artifact visualEvidenceArtifact) error {
	var artifacts []visualEvidenceArtifact
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(content, &artifacts); err != nil {
			return fmt.Errorf("parse run-scoped Visual Evidence state: %w", err)
		}
	}
	for index, existing := range artifacts {
		if existing.Path == artifact.Path && existing.Kind == artifact.Kind {
			artifacts[index] = artifact
			return writeJSONFile(path, artifacts)
		}
	}
	artifacts = append(artifacts, artifact)
	return writeJSONFile(path, artifacts)
}

func readRunScopedVisualEvidence(context runContext) ([]visualEvidenceArtifact, error) {
	if err := ensureWorkspacePathHasNoSymlinkAncestor(context.StateDir, runScopedVisualEvidenceStateName); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(filepath.Join(context.StateDir, runScopedVisualEvidenceStateName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var artifacts []visualEvidenceArtifact
	if err := json.Unmarshal(content, &artifacts); err != nil {
		return nil, fmt.Errorf("parse run-scoped Visual Evidence state: %w", err)
	}
	return artifacts, nil
}

func ensureRunHasScopedHostLock(repoDir string, manifest runManifest, runID string) error {
	if err := validateHostID(manifest.Host); err != nil {
		return err
	}
	if err := ensureOutputPathHasNoSymlinkParent(repoDir, filepath.Join(".maya-stall", "state", "locks", "hosts")); err != nil {
		return err
	}
	lockPath := filepath.Join(repoDir, ".maya-stall", "state", "locks", "hosts", manifest.Host+".lock")
	kind, owner, stale, found, err := readHostLockRunOwner(lockPath)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("host lock for %s is not owned by an active or kept run", manifest.Host)
	}
	if owner != runID {
		return fmt.Errorf("host lock for %s belongs to %s run %s, not %s", manifest.Host, kind, owner, runID)
	}
	if stale {
		return fmt.Errorf("host lock for %s belongs to stale active run %s", manifest.Host, runID)
	}
	return nil
}

func readHostLockRunOwner(lockPath string) (kind string, runID string, stale bool, found bool, err error) {
	info, err := os.Lstat(lockPath)
	if os.IsNotExist(err) {
		return "", "", false, false, nil
	}
	if err != nil {
		return "", "", false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", false, false, fmt.Errorf("host lock %s must not be a symlink", lockPath)
	}
	if !info.Mode().IsRegular() {
		return "", "", false, false, fmt.Errorf("host lock %s must be a regular file", lockPath)
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		return "", "", false, false, err
	}
	var activeRun string
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "keptRun":
			value := strings.TrimSpace(value)
			if value != "" {
				return "kept", value, false, true, nil
			}
		case "activeRun":
			activeRun = strings.TrimSpace(value)
		}
	}
	if activeRun == "" {
		return "", "", false, false, nil
	}
	stale, err = isStaleHostLock(lockPath)
	if err != nil {
		return "", "", false, false, err
	}
	return "active", activeRun, stale, true, nil
}
