package cli

import (
	"path/filepath"
)

type runWorkspace struct {
	repoDir        string
	runID          string
	remoteWorkRoot string
	scenarioResult string
}

func newRunWorkspace(repoDir string, runID string, remoteWorkRoot string, scenarioResult string) (runWorkspace, error) {
	cleanScenarioResult, err := cleanScenarioPath(scenarioResult)
	if err != nil {
		return runWorkspace{}, err
	}
	return runWorkspace{
		repoDir:        repoDir,
		runID:          runID,
		remoteWorkRoot: remotePath(remoteWorkRoot),
		scenarioResult: cleanScenarioResult,
	}, nil
}

func (workspace runWorkspace) RunID() string {
	return workspace.runID
}

func (workspace runWorkspace) StateDir() string {
	return filepath.Join(workspace.repoDir, ".maya-stall", "state", "runs", workspace.runID)
}

func (workspace runWorkspace) EvidenceDir() string {
	return filepath.Join(workspace.repoDir, "artifacts", "maya-stall", workspace.runID)
}

func (workspace runWorkspace) LocalWorkspace() string {
	return filepath.Join(workspace.StateDir(), "workspace")
}

func (workspace runWorkspace) LocalPayloadRoot() string {
	return filepath.Join(workspace.StateDir(), "payload")
}

func (workspace runWorkspace) LocalPayloadPath(item manifestPayload) string {
	return filepath.Join(workspace.StateDir(), item.Staged)
}

func (workspace runWorkspace) EventsPath() string {
	return filepath.Join(workspace.StateDir(), "events.jsonl")
}

func (workspace runWorkspace) LogPath() string {
	return filepath.Join(workspace.StateDir(), "logs", "session.log")
}

func (workspace runWorkspace) LocalScenarioResultPath() string {
	return filepath.Join(workspace.LocalWorkspace(), workspace.scenarioResult)
}

func (workspace runWorkspace) RemoteRunsRoot() string {
	return remoteJoin(workspace.remoteWorkRoot, "runs")
}

func (workspace runWorkspace) RemoteRunRoot() string {
	return remoteJoin(workspace.RemoteRunsRoot(), workspace.runID)
}

func (workspace runWorkspace) RemoteWorkspace() string {
	return remoteJoin(workspace.RemoteRunRoot(), "workspace")
}

func (workspace runWorkspace) RemotePayloadPath(item manifestPayload) string {
	return remoteJoin(workspace.RemoteRunRoot(), item.Staged)
}

func (workspace runWorkspace) remotePayloadKindPath(kind string, source string) (string, error) {
	if err := rejectSFTPRepoPath(source); err != nil {
		return "", err
	}
	clean, err := cleanRepoRelativePath(source)
	if err != nil {
		return "", err
	}
	return remoteJoin(workspace.RemoteRunRoot(), "payload", kind, clean), nil
}

func (workspace runWorkspace) RemoteScenarioResultPath() string {
	return remoteJoin(workspace.RemoteWorkspace(), workspace.scenarioResult)
}

func (workspace runWorkspace) RemoteScenarioWrapperPath() string {
	return remoteJoin(workspace.RemoteWorkspace(), ".maya-stall-scenario.py")
}

func (workspace runWorkspace) RemoteRunModulesRoot() string {
	return workspace.RemoteRunsRoot()
}

func (workspace runWorkspace) RemoteOutputPath(relativePath string) string {
	return remoteJoin(workspace.RemoteWorkspace(), relativePath)
}

func remotePath(path string) string {
	return filepath.ToSlash(path)
}
