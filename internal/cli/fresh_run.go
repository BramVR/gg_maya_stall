package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type freshRun interface {
	Run() (runOutcome, error)
}

type freshRunLifecycle struct {
	repoDir         string
	options         runOptions
	runtime         runRuntime
	configPath      string
	scenario        scenarioContract
	host            hostRuntime
	resolvedRuntime resolvedRuntime
	context         runContext
	manifest        runManifest
	brokerResult    ScenarioResult
	result          ScenarioResult
	visualEvidence  []visualEvidenceArtifact
	validatorResult []validatorResult
	stopPolicy      string
	followUp        []string
	releaseHostLock bool
}

func newFreshRun(repoDir string, options runOptions, runtime runRuntime) freshRun {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return &freshRunLifecycle{
		repoDir:    repoDir,
		options:    options,
		runtime:    runtime,
		stopPolicy: "stopped",
	}
}

func (run *freshRunLifecycle) Run() (outcome runOutcome, err error) {
	defer func() {
		if run.releaseHostLock {
			if releaseErr := run.host.release(); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release Host Lock for %s: %w", run.host.HostID, releaseErr))
			}
		}
	}()

	if err := run.setup(); err != nil {
		return runOutcome{}, err
	}
	defer func() {
		if err == nil && outcome.StopPolicy == "stopped" {
			if cleanupErr := cleanupRunState(run.repoDir, outcome.RunID); cleanupErr != nil {
				err = fmt.Errorf("clean up Fresh Run state for %s: %w", outcome.RunID, cleanupErr)
			}
		}
	}()

	if err := run.execute(); err != nil {
		return runOutcome{}, err
	}
	outcome, err = run.settle()
	return outcome, err
}

func (run *freshRunLifecycle) setup() error {
	config, configPath, err := loadRepoRunConfig(run.repoDir)
	if err != nil {
		return err
	}
	run.configPath = configPath
	scenario, err := resolveScenarioContract(config, run.options.ScenarioName)
	if err != nil {
		return err
	}
	run.scenario = scenario
	host, err := selectHostForRun(run.repoDir, run.options)
	if err != nil {
		return err
	}
	if host.release == nil {
		host.release = func() error { return nil }
	}
	run.host = host
	run.releaseHostLock = true

	resolved, err := resolveRuntimeForHost(host.Config)
	if err != nil {
		return err
	}
	run.resolvedRuntime = resolved
	if run.runtime.Host == nil {
		run.runtime.Host = resolved.Host
	}
	if err := rejectMismatchedRuntimeOverride(resolved, run.runtime); err != nil {
		return err
	}
	if run.runtime.Broker == nil {
		run.runtime.Broker = resolved.Broker
	}
	if err := rejectInvalidSessionBroker(run.runtime.Broker); err != nil {
		return err
	}
	if err := rejectUnsupportedEvidenceConfig(run.runtime.Broker, scenario.Config); err != nil {
		return err
	}

	runID := run.runtime.Now().UTC().Format("20060102T150405.000000000Z")
	workspace, err := newRunWorkspace(run.repoDir, runID, host.Config.WorkRoot, scenario.ScenarioResultPath)
	if err != nil {
		return err
	}
	run.context = runContext{
		RepoDir:            run.repoDir,
		RunWorkspace:       workspace,
		StateDir:           workspace.StateDir(),
		EvidenceDir:        workspace.EvidenceDir(),
		Workspace:          workspace.LocalWorkspace(),
		EventsPath:         workspace.EventsPath(),
		LogPath:            workspace.LogPath(),
		ScenarioResultPath: workspace.LocalScenarioResultPath(),
		Environment: map[string]string{
			scenarioResultEnvVar: workspace.LocalScenarioResultPath(),
		},
	}
	if root := trustedPluginArtifactsRoot(host.Config); root != "" {
		run.context.Environment[trustedPluginArtifactsRootEnvVar] = root
	}
	if err := createCleanRunDirs(run.context); err != nil {
		return err
	}
	if err := run.runtime.Host.StagePayload(run.context, scenario.Payload); err != nil {
		return err
	}

	run.manifest = runManifest{
		RunID:         runID,
		Scenario:      scenario.Name,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		Runtime:       resolved.Metadata,
		ConfigPath:    repoRelativePath(run.repoDir, configPath),
		Payload:       scenario.Payload,
	}
	if err := writeJSONFile(filepath.Join(run.context.StateDir, "manifest.json"), run.manifest); err != nil {
		return err
	}
	if err := writeRunRetentionRecord(run.context, newRunRetentionRecord(run.context, run.manifest, host.Config, "running", "")); err != nil {
		return err
	}
	if err := markHostLockActive(run.repoDir, run.host.HostID, run.manifest.RunID); err != nil {
		return err
	}
	return appendEvent(run.context.EventsPath, "run.started", scenario.Name)
}

func (run *freshRunLifecycle) execute() error {
	result, err := run.runtime.Broker.RunScenario(run.context, run.scenario.Config)
	if err != nil {
		if evidenceErr := run.collectPreResultFailureEvidence(err); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		return err
	}
	run.brokerResult = result
	run.result = result
	return nil
}

func (run *freshRunLifecycle) collectPreResultFailureEvidence(runErr error) error {
	if err := ensureFailureLog(run.context, runErr); err != nil {
		return err
	}
	summary := fmt.Sprintf("Scenario failed before result collection: %v", runErr)
	if err := appendEvent(run.context.EventsPath, "run.failed-before-result-collection", summary); err != nil {
		return err
	}
	if collector, ok := run.runtime.Host.(failureArtifactCollector); ok {
		if err := collector.CollectFailureArtifacts(run.context, run.scenario); err != nil {
			if eventErr := appendEvent(run.context.EventsPath, "run.failed-before-result-collection.artifact-collection-failed", err.Error()); eventErr != nil {
				return eventErr
			}
		} else if err := appendEvent(run.context.EventsPath, "run.failed-before-result-collection.artifacts-collected", "best-effort"); err != nil {
			return err
		}
	} else if collector, ok := run.runtime.Host.(artifactCollector); ok {
		if err := collector.CollectArtifacts(run.context, run.scenario); err != nil {
			if eventErr := appendEvent(run.context.EventsPath, "run.failed-before-result-collection.artifact-collection-failed", err.Error()); eventErr != nil {
				return eventErr
			}
		} else if err := appendEvent(run.context.EventsPath, "run.failed-before-result-collection.artifacts-collected", "best-effort"); err != nil {
			return err
		}
	}
	result := ScenarioResult{Status: resultStatusFailed, Summary: summary}
	if err := preserveCollectedScenarioResultOrWriteFailure(run.context, run.scenario.ScenarioResultPath, result); err != nil {
		return err
	}
	var artifacts []visualEvidenceArtifact
	if capturer, ok := run.runtime.Broker.(screenshotCapturer); ok && run.scenario.Config.Evidence.Screenshots.Enabled {
		artifact, err := capturer.CaptureScreenshot(run.context, screenshotRequest{Name: "failure-desktop.png"})
		if err == nil {
			artifacts = append(artifacts, artifact)
			annotateVisualEvidenceTarget(artifacts, run.manifest.TargetProfile, run.manifest.Host)
			if eventErr := appendEvent(run.context.EventsPath, "visual-evidence.failure-screenshot", artifact.Path); eventErr != nil {
				return eventErr
			}
		} else if eventErr := appendEvent(run.context.EventsPath, "visual-evidence.failure-screenshot.failed", err.Error()); eventErr != nil {
			return eventErr
		}
	}
	return writeEvidenceBundle(run.context, run.manifest, run.scenario, result, artifacts, nil)
}

func preserveCollectedScenarioResultOrWriteFailure(context runContext, resultPath string, result ScenarioResult) error {
	if err := validateScenarioResultPath(context, resultPath); err != nil {
		return err
	}
	_, found, err := readScenarioResultDocument(context.ScenarioResultPath)
	if err != nil {
		if eventErr := appendEvent(context.EventsPath, "run.failed-before-result-collection.scenario-result-unreadable", err.Error()); eventErr != nil {
			return eventErr
		}
		found = false
	}
	if found {
		return copyFile(context.ScenarioResultPath, filepath.Join(context.EvidenceDir, evidenceScenarioResultFileName))
	}
	return writeScenarioResult(context, resultPath, newScenarioResultDocument(result))
}

func ensureFailureLog(context runContext, runErr error) error {
	if _, err := os.Stat(context.LogPath); err == nil {
		return appendFile(context.LogPath, fmt.Sprintf("Scenario failed before result collection: %v\n", runErr))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(context.LogPath, []byte(fmt.Sprintf("Scenario failed before result collection: %v\n", runErr)), 0o644)
}

func appendFile(path string, content string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = file.WriteString(content)
	return err
}

func (run *freshRunLifecycle) settle() (runOutcome, error) {
	if collector, ok := run.runtime.Host.(artifactCollector); ok {
		if err := collector.CollectArtifacts(run.context, run.scenario); err != nil {
			return runOutcome{}, err
		}
	}
	if err := validateScenarioResultPath(run.context, run.scenario.ScenarioResultPath); err != nil {
		return runOutcome{}, err
	}
	resultDocument, found, err := readScenarioResultDocument(run.context.ScenarioResultPath)
	if err != nil {
		return runOutcome{}, err
	}
	if found {
		run.result = resultDocument.Result
		if run.result.Status == "" {
			run.result.Status = run.brokerResult.Status
		}
		if run.result.Summary == "" {
			run.result.Summary = run.brokerResult.Summary
		}
	} else {
		resultDocument = newScenarioResultDocument(run.result)
	}
	if run.result.Status == "" {
		run.result.Status = resultStatusPassed
	}
	run.visualEvidence, err = collectScenarioVisualEvidence(run.runtime.Broker, run.context, run.scenario.Name, run.scenario.Config.Evidence)
	if err != nil {
		return runOutcome{}, err
	}
	annotateVisualEvidenceTarget(run.visualEvidence, run.manifest.TargetProfile, run.manifest.Host)
	runScopedVisualEvidence, err := readRunScopedVisualEvidence(run.context)
	if err != nil {
		return runOutcome{}, err
	}
	run.visualEvidence = mergeVisualEvidence(run.visualEvidence, runScopedVisualEvidence)
	resultDocument.setResult(run.result)
	if err := writeScenarioResult(run.context, run.scenario.ScenarioResultPath, resultDocument); err != nil {
		return runOutcome{}, err
	}
	run.validatorResult, err = validateRunOutputs(run.context, run.scenario, run.result)
	if err != nil {
		return runOutcome{}, err
	}
	if hasValidatorFailure(run.validatorResult) {
		run.result.Status = resultStatusFailed
	}
	resultDocument.setResult(run.result)
	if err := writeScenarioResult(run.context, run.scenario.ScenarioResultPath, resultDocument); err != nil {
		return runOutcome{}, err
	}
	if !shouldStopAfter(run.options.StopAfter, run.result.Status) {
		run.stopPolicy = "kept"
		run.followUp = append(run.followUp,
			fmt.Sprintf("maya-stall status --run %s", run.manifest.RunID),
			fmt.Sprintf("maya-stall attach %s", run.manifest.RunID),
			fmt.Sprintf("maya-stall stop %s", run.manifest.RunID),
		)
	}
	if err := appendEvent(run.context.EventsPath, "run.completed", run.result.Status); err != nil {
		return runOutcome{}, err
	}
	if err := writeEvidenceBundle(run.context, run.manifest, run.scenario, run.result, run.visualEvidence, run.validatorResult); err != nil {
		return runOutcome{}, err
	}
	if run.stopPolicy == "kept" {
		retention, ok := run.runtime.Broker.(runRetentionBroker)
		if !ok || !retention.RetentionCapabilities().RetainOnFailure {
			return runOutcome{}, unsupportedBrokerCapabilityError(run.manifest.Runtime.BrokerAdapter, "retain-on-failure")
		}
		reason := "stop-after-" + run.options.StopAfter
		if run.options.StopAfter == stopAfterSuccess && run.result.Status != resultStatusPassed {
			reason = "keep-on-failure"
		}
		if err := markHostLockKept(run.repoDir, run.host.HostID, run.manifest.RunID); err != nil {
			return runOutcome{}, err
		}
		run.releaseHostLock = false
		session, err := retention.RetainRun(run.context, run.manifest, reason)
		if err != nil {
			return runOutcome{}, err
		}
		record := newRunRetentionRecord(run.context, run.manifest, run.host.Config, "kept", reason)
		record.BrokerCapabilities = retention.RetentionCapabilities()
		record.RemoteSession = session
		if err := writeRunRetentionRecord(run.context, record); err != nil {
			return runOutcome{}, err
		}
	} else if retention, ok := run.runtime.Broker.(runRetentionBroker); ok && retention.RetentionCapabilities().CleanupRetainedWorkspace {
		if err := retention.CleanupRun(run.context); err != nil {
			return runOutcome{}, fmt.Errorf("clean up remote run workspace for %s: %w", run.manifest.RunID, err)
		}
	}
	return runOutcome{
		RunID:            run.manifest.RunID,
		Scenario:         run.scenario.Name,
		TargetProfile:    run.host.TargetProfile,
		Host:             run.host.HostID,
		StateDir:         run.context.StateDir,
		EvidenceDir:      run.context.EvidenceDir,
		Result:           run.result,
		Validators:       run.validatorResult,
		StopPolicy:       run.stopPolicy,
		FollowUpCommands: run.followUp,
	}, nil
}
