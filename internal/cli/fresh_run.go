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
	repoDir                      string
	options                      runOptions
	runtime                      runRuntime
	configPath                   string
	scenario                     scenarioContract
	host                         hostRuntime
	resolvedRuntime              resolvedRuntime
	context                      runContext
	manifest                     runManifest
	session                      brokerSessionIdentity
	sessionStarted               bool
	sessionSettled               bool
	brokerResult                 ScenarioResult
	result                       ScenarioResult
	visualEvidence               []visualEvidenceArtifact
	validatorResult              []validatorResult
	stopPolicy                   string
	followUp                     []string
	releaseHostLock              bool
	skipSettleArtifactCollection bool
	skipSettleVisualEvidence     bool
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
		if run.sessionStarted && !run.sessionSettled {
			if stopErr := run.runtime.Broker.StopSession(run.context, run.session); stopErr != nil {
				err = errors.Join(err, fmt.Errorf("stop Maya UI Session for %s after run failure: %w", run.manifest.RunID, stopErr))
				run.releaseHostLock = false
			} else if syncErr := run.syncEvidenceEvents(); syncErr != nil {
				err = errors.Join(err, syncErr)
			}
		}
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
	if err := validateScenarioInputs(run.repoDir, scenario); err != nil {
		return err
	}
	if err := validateScenarioRemotePaths(scenario.Config); err != nil {
		return err
	}
	host, err := selectHostForRun(run.repoDir, run.options)
	if err != nil {
		return err
	}
	if host.release == nil {
		host.release = func() error { return nil }
	}
	run.host = host
	run.releaseHostLock = true
	if err := validateTrustedPluginArtifactsRoot(host.Config); err != nil {
		return err
	}

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
	if err := ensureTrustedPluginArtifactsAllowlistedForRun(host.Config, scenario); err != nil {
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
	if err := appendEvent(run.context.EventsPath, "run.started", scenario.Name); err != nil {
		return err
	}
	if err := run.startFreshSession(); err != nil {
		return err
	}
	return run.runtime.Host.StagePayload(run.context, scenario.Payload)
}

func (run *freshRunLifecycle) startFreshSession() error {
	session, err := run.runtime.Broker.StartFreshSession(run.context, run.scenario.Config)
	if session.BrokerAdapter != "" {
		run.session = session
		run.sessionStarted = true
		run.manifest.BrokerSession = &session
		if manifestErr := writeJSONFile(filepath.Join(run.context.StateDir, "manifest.json"), run.manifest); manifestErr != nil {
			return errors.Join(err, manifestErr)
		}
	}
	if err != nil {
		if evidenceErr := ensureFailureLog(run.context, err); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		if evidenceErr := appendEvent(run.context.EventsPath, "run.failed-before-result-collection", err.Error()); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		if evidenceErr := run.writeBrokerFailureEvidence(err); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		return err
	}
	return nil
}

func (run *freshRunLifecycle) execute() error {
	result, err := run.runtime.Broker.RunScenario(run.context, run.scenario.Config)
	if err != nil {
		if evidenceErr := run.collectBrokerFailureArtifacts(err); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		recovered, recoveryErr := run.recoverCompletedScenarioAfterBrokerFailure(err)
		if recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
		if recovered {
			return nil
		}
		if evidenceErr := run.writeBrokerFailureEvidence(err); evidenceErr != nil {
			return errors.Join(err, evidenceErr)
		}
		return err
	}
	run.brokerResult = result
	run.result = result
	return nil
}

func (run *freshRunLifecycle) collectBrokerFailureArtifacts(runErr error) error {
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
	return nil
}

func (run *freshRunLifecycle) recoverCompletedScenarioAfterBrokerFailure(runErr error) (bool, error) {
	if err := validateScenarioResultPath(run.context, run.scenario.ScenarioResultPath); err != nil {
		return false, err
	}
	resultDocument, found, err := readScenarioResultDocument(run.context.ScenarioResultPath)
	if err != nil {
		if eventErr := appendEvent(run.context.EventsPath, "run.recovered-after-broker-failure.scenario-result-unreadable", err.Error()); eventErr != nil {
			return false, eventErr
		}
		return false, nil
	}
	if !found {
		if err := appendEvent(run.context.EventsPath, "run.recovered-after-broker-failure.scenario-result-missing", runErr.Error()); err != nil {
			return false, err
		}
		return false, nil
	}
	if resultDocument.Result.Status != resultStatusPassed {
		detail := resultDocument.Result.Status
		if detail == "" {
			detail = "missing"
		}
		if err := appendEvent(run.context.EventsPath, "run.recovered-after-broker-failure.rejected", "Scenario Result status "+detail); err != nil {
			return false, err
		}
		return false, nil
	}
	run.result = resultDocument.Result
	run.brokerResult = ScenarioResult{Status: resultStatusPassed, Summary: "Scenario completed before broker failure"}
	run.skipSettleArtifactCollection = true
	run.skipSettleVisualEvidence = true
	return true, appendEvent(run.context.EventsPath, "run.recovered-after-broker-failure", runErr.Error())
}

func (run *freshRunLifecycle) writeBrokerFailureEvidence(runErr error) error {
	summary := fmt.Sprintf("Scenario failed before result collection: %v", runErr)
	result := ScenarioResult{Status: resultStatusFailed, Summary: summary}
	if err := preserveCollectedScenarioResultOrWriteFailure(run.context, run.scenario.ScenarioResultPath, result); err != nil {
		return err
	}
	artifacts, err := run.captureFailureScreenshot()
	if err != nil {
		return err
	}
	return writeEvidenceBundle(run.context, run.manifest, run.scenario, result, artifacts, nil)
}

func (run *freshRunLifecycle) captureFailureScreenshot() ([]visualEvidenceArtifact, error) {
	var artifacts []visualEvidenceArtifact
	if capturer, ok := run.runtime.Broker.(screenshotCapturer); ok && run.scenario.Config.Evidence.Screenshots.Enabled {
		artifact, err := capturer.CaptureScreenshot(run.context, screenshotRequest{Name: "failure-desktop.png"})
		if err == nil {
			artifacts = append(artifacts, artifact)
			annotateVisualEvidenceTarget(artifacts, run.manifest.TargetProfile, run.manifest.Host)
			if eventErr := appendEvent(run.context.EventsPath, "visual-evidence.failure-screenshot", artifact.Path); eventErr != nil {
				return nil, eventErr
			}
		} else if eventErr := appendEvent(run.context.EventsPath, "visual-evidence.failure-screenshot.failed", err.Error()); eventErr != nil {
			return nil, eventErr
		}
	}
	return artifacts, nil
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
	if collector, ok := run.runtime.Host.(artifactCollector); ok && !run.skipSettleArtifactCollection {
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
	if !run.skipSettleVisualEvidence {
		run.visualEvidence, err = collectScenarioVisualEvidence(run.runtime.Broker, run.context, run.scenario.Name, run.scenario.Config.Evidence)
		if err != nil {
			return runOutcome{}, err
		}
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
	validatorFailed := hasValidatorFailure(run.validatorResult)
	if validatorFailed {
		run.result.Status = resultStatusFailed
	}
	resultDocument.setResult(run.result)
	if err := writeScenarioResult(run.context, run.scenario.ScenarioResultPath, resultDocument); err != nil {
		return runOutcome{}, err
	}
	if validatorFailed && run.skipSettleVisualEvidence {
		artifacts, err := run.captureFailureScreenshot()
		if err != nil {
			return runOutcome{}, err
		}
		run.visualEvidence = mergeVisualEvidence(run.visualEvidence, artifacts)
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
	if run.stopPolicy == "stopped" {
		if err := run.runtime.Broker.StopSession(run.context, run.session); err != nil {
			return runOutcome{}, fmt.Errorf("stop Maya UI Session for %s: %w", run.manifest.RunID, err)
		}
		run.sessionSettled = true
		if err := run.syncEvidenceEvents(); err != nil {
			return runOutcome{}, err
		}
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
		session, err := retention.RetainRun(run.context, run.manifest, reason)
		if err != nil {
			return runOutcome{}, err
		}
		if session.BrokerAdapter != run.session.BrokerAdapter || session.SessionID != run.session.SessionID {
			return runOutcome{}, fmt.Errorf("Session Broker retained a different Maya UI Session: started %s/%s, retained %s/%s", run.session.BrokerAdapter, run.session.SessionID, session.BrokerAdapter, session.SessionID)
		}
		record := newRunRetentionRecord(run.context, run.manifest, run.host.Config, "kept", reason)
		record.BrokerCapabilities = retention.RetentionCapabilities()
		record.RemoteSession = session
		if err := writeRunRetentionRecord(run.context, record); err != nil {
			return runOutcome{}, err
		}
		run.sessionSettled = true
		run.releaseHostLock = false
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

func (run *freshRunLifecycle) syncEvidenceEvents() error {
	if _, err := os.Stat(run.context.EvidenceDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	events, err := os.ReadFile(run.context.EventsPath)
	if err != nil {
		return fmt.Errorf("read run events after stopping failed run: %w", err)
	}
	if err := os.WriteFile(filepath.Join(run.context.EvidenceDir, evidenceEventsFileName), events, 0o644); err != nil {
		return fmt.Errorf("update Evidence Bundle events after stopping failed run: %w", err)
	}
	return nil
}
