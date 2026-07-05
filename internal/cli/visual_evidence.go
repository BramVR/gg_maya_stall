package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type visualEvidenceOptions struct {
	HostConfig    string
	TargetProfile string
	HostPin       string
}

func parseVisualEvidenceArgs(args []string) (visualEvidenceOptions, error) {
	options := visualEvidenceOptions{TargetProfile: "default"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--host-config":
			i++
			if i >= len(args) || args[i] == "" {
				return visualEvidenceOptions{}, newUsageError("--host-config needs a path")
			}
			options.HostConfig = args[i]
		case "--target-profile":
			i++
			if i >= len(args) || args[i] == "" {
				return visualEvidenceOptions{}, newUsageError("--target-profile needs a name")
			}
			options.TargetProfile = args[i]
		case "--host":
			i++
			if i >= len(args) || args[i] == "" {
				return visualEvidenceOptions{}, newUsageError("--host needs a Maya Host id")
			}
			options.HostPin = args[i]
		default:
			return visualEvidenceOptions{}, newUsageError("unknown visual evidence option %q", arg)
		}
	}
	return options, nil
}

func captureStandaloneVisualEvidence(repoDir string, options visualEvidenceOptions, runtime runRuntime, kind string) (outcome runOutcome, artifact visualEvidenceArtifact, err error) {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	host, err := selectHostForRun(repoDir, runOptions{
		HostConfig:    options.HostConfig,
		TargetProfile: options.TargetProfile,
		HostPin:       options.HostPin,
	})
	if err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if runtime.Broker == nil {
		runtime.Broker = sessionBrokerForConfig(host.Config)
	}
	defer func() {
		if host.release != nil {
			if releaseErr := host.release(); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release Host Lock for %s: %w", host.HostID, releaseErr))
			}
		}
	}()

	runID := runtime.Now().UTC().Format("20060102T150405.000000000Z")
	stateDir := filepath.Join(repoDir, ".maya-stall", "state", "runs", runID)
	evidenceDir := filepath.Join(repoDir, "artifacts", "maya-stall", runID)
	context := runContext{
		RepoDir:     repoDir,
		StateDir:    stateDir,
		EvidenceDir: evidenceDir,
		Workspace:   filepath.Join(stateDir, "workspace"),
		EventsPath:  filepath.Join(stateDir, "events.jsonl"),
		LogPath:     filepath.Join(stateDir, "logs", "session.log"),
	}
	if err := createCleanRunDirs(context); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	cleanupState := true
	defer func() {
		if cleanupState {
			_ = cleanupRunState(repoDir, runID)
			if err != nil {
				_ = os.RemoveAll(evidenceDir)
			}
		}
	}()

	manifest := runManifest{
		RunID:         runID,
		Scenario:      "manual-" + kind,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
	}
	if configPath, err := DiscoverConfig(repoDir); err == nil {
		manifest.ConfigPath = repoRelativePath(repoDir, configPath)
	}
	if err := writeJSONFile(filepath.Join(stateDir, "manifest.json"), manifest); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "visual-evidence.started", kind); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	brokerName := sessionBrokerDisplayName(runtime.Broker)
	if err := os.WriteFile(context.LogPath, []byte(brokerName+" captured Visual Evidence\n"), 0o644); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}

	switch kind {
	case "screenshot":
		capturer, ok := runtime.Broker.(screenshotCapturer)
		if !ok {
			return runOutcome{}, visualEvidenceArtifact{}, fmt.Errorf("Session Broker does not support screenshot capture")
		}
		artifact, err = capturer.CaptureScreenshot(context, screenshotRequest{Name: "screenshot.png"})
	case "recording":
		capturer, ok := runtime.Broker.(recordingCapturer)
		if !ok {
			return runOutcome{}, visualEvidenceArtifact{}, fmt.Errorf("Session Broker does not support recording capture")
		}
		artifact, err = capturer.CaptureRecording(context, recordingRequest{Name: "recording.mp4", Duration: defaultRecordingDuration, FPS: defaultRecordingFPS})
	default:
		err = fmt.Errorf("unknown Visual Evidence kind %q", kind)
	}
	if err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	result := ScenarioResult{Status: resultStatusPassed, Summary: brokerName + " Visual Evidence captured"}
	if err := writeJSONFile(filepath.Join(evidenceDir, "scenario-result.json"), result); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "visual-evidence.completed", artifact.Path); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if err := writeEvidenceBundle(context, manifest, scenarioConfig{}, result, []visualEvidenceArtifact{artifact}, nil); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	if err := cleanupRunState(repoDir, runID); err != nil {
		return runOutcome{}, visualEvidenceArtifact{}, err
	}
	cleanupState = false
	return runOutcome{
		RunID:         runID,
		Scenario:      manifest.Scenario,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		StateDir:      stateDir,
		EvidenceDir:   evidenceDir,
		Result:        result,
		StopPolicy:    "stopped",
	}, artifact, nil
}
