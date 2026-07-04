package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const resultStatusPassed = "passed"

type runRuntime struct {
	Host   runHost
	Broker sessionBroker
	Now    func() time.Time
}

type runHost interface {
	StagePayload(runContext, []manifestPayload) error
}

type sessionBroker interface {
	RunScenario(runContext, scenarioConfig) (ScenarioResult, error)
}

type runContext struct {
	RepoDir     string
	StateDir    string
	EvidenceDir string
	Workspace   string
	EventsPath  string
	LogPath     string
}

type runOutcome struct {
	RunID         string
	Scenario      string
	TargetProfile string
	Host          string
	StateDir      string
	EvidenceDir   string
	Result        ScenarioResult
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

type evidenceBundle struct {
	RunID          string            `json:"runId"`
	Scenario       string            `json:"scenario"`
	Status         string            `json:"status"`
	TargetProfile  string            `json:"targetProfile"`
	Host           string            `json:"host"`
	Manifest       string            `json:"manifest"`
	Events         string            `json:"events"`
	Log            string            `json:"log"`
	ScenarioResult string            `json:"scenarioResult"`
	Payload        []manifestPayload `json:"payload"`
}

func runScenario(repoDir string, options runOptions, runtime runRuntime) (outcome runOutcome, err error) {
	if runtime.Host == nil {
		runtime.Host = fakeHost{}
	}
	if runtime.Broker == nil {
		runtime.Broker = fakeSessionBroker{Result: ScenarioResult{Status: resultStatusPassed, Summary: "fake Scenario completed"}}
	}
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
	defer func() {
		if releaseErr := host.release(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release Host Lock for %s: %w", host.HostID, releaseErr))
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
	if result.Status == "" {
		result.Status = resultStatusPassed
	}
	if err := writeScenarioResult(context, scenarioResultPath, result); err != nil {
		return runOutcome{}, err
	}
	if err := appendEvent(context.EventsPath, "run.completed", result.Status); err != nil {
		return runOutcome{}, err
	}
	if err := writeEvidenceBundle(context, manifest, result); err != nil {
		return runOutcome{}, err
	}

	return runOutcome{
		RunID:         runID,
		Scenario:      options.ScenarioName,
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		StateDir:      stateDir,
		EvidenceDir:   evidenceDir,
		Result:        result,
	}, nil
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
		{kind: "scripts", paths: payload.Scripts},
		{kind: "scenes", paths: payload.Scenes},
		{kind: "pluginArtifacts", paths: payload.PluginArtifacts},
	} {
		for _, source := range item.paths {
			cleanSource, err := cleanRepoRelativePath(source)
			if err != nil {
				return nil, err
			}
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

func writeScenarioResult(context runContext, resultPath string, result ScenarioResult) error {
	workspaceResult := filepath.Join(context.Workspace, resultPath)
	if err := writeJSONFile(workspaceResult, result); err != nil {
		return err
	}
	return copyFile(workspaceResult, filepath.Join(context.EvidenceDir, "scenario-result.json"))
}

func writeEvidenceBundle(context runContext, manifest runManifest, result ScenarioResult) error {
	if err := copyFile(filepath.Join(context.StateDir, "manifest.json"), filepath.Join(context.EvidenceDir, "manifest.json")); err != nil {
		return err
	}
	if err := copyFile(context.EventsPath, filepath.Join(context.EvidenceDir, "events.jsonl")); err != nil {
		return err
	}
	if err := copyFile(context.LogPath, filepath.Join(context.EvidenceDir, "logs", "session.log")); err != nil {
		return err
	}
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
	}
	return writeJSONFile(filepath.Join(context.EvidenceDir, "evidence.json"), bundle)
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
