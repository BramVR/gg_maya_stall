package cli

import (
	"path/filepath"
	"testing"
)

func TestRunWorkspaceDerivesLocalAndRemotePaths(t *testing.T) {
	repoDir := filepath.Join(string(filepath.Separator), "repo")

	workspace, err := newRunWorkspace(repoDir, "run-1", `C:\maya-stall`, "outputs/result.json")
	if err != nil {
		t.Fatalf("newRunWorkspace returned error: %v", err)
	}

	if workspace.StateDir() != filepath.Join(repoDir, ".maya-stall", "state", "runs", "run-1") {
		t.Fatalf("StateDir = %q", workspace.StateDir())
	}
	if workspace.EvidenceDir() != filepath.Join(repoDir, "artifacts", "maya-stall", "run-1") {
		t.Fatalf("EvidenceDir = %q", workspace.EvidenceDir())
	}
	if workspace.LocalWorkspace() != filepath.Join(workspace.StateDir(), "workspace") {
		t.Fatalf("LocalWorkspace = %q", workspace.LocalWorkspace())
	}
	if workspace.LocalPayloadRoot() != filepath.Join(workspace.StateDir(), "payload") {
		t.Fatalf("LocalPayloadRoot = %q", workspace.LocalPayloadRoot())
	}
	if workspace.LocalScenarioResultPath() != filepath.Join(workspace.LocalWorkspace(), "outputs", "result.json") {
		t.Fatalf("LocalScenarioResultPath = %q", workspace.LocalScenarioResultPath())
	}
	if workspace.RemoteRunRoot() != "C:/maya-stall/runs/run-1" {
		t.Fatalf("RemoteRunRoot = %q", workspace.RemoteRunRoot())
	}
	if workspace.RemoteWorkspace() != "C:/maya-stall/runs/run-1/workspace" {
		t.Fatalf("RemoteWorkspace = %q", workspace.RemoteWorkspace())
	}
	if workspace.RemotePayloadPath(manifestPayload{Kind: "mayaScripts", Source: "maya/smoke.py", Staged: filepath.Join("payload", "mayaScripts", "maya", "smoke.py")}) != "C:/maya-stall/runs/run-1/payload/mayaScripts/maya/smoke.py" {
		t.Fatalf("RemotePayloadPath = %q", workspace.RemotePayloadPath(manifestPayload{Staged: filepath.Join("payload", "mayaScripts", "maya", "smoke.py")}))
	}
	if workspace.RemoteScenarioResultPath() != "C:/maya-stall/runs/run-1/workspace/outputs/result.json" {
		t.Fatalf("RemoteScenarioResultPath = %q", workspace.RemoteScenarioResultPath())
	}
	if workspace.RemoteScenarioWrapperPath() != "C:/maya-stall/runs/run-1/workspace/.maya-stall-scenario.py" {
		t.Fatalf("RemoteScenarioWrapperPath = %q", workspace.RemoteScenarioWrapperPath())
	}
	if workspace.RemoteRunModulesRoot() != "C:/maya-stall/runs" {
		t.Fatalf("RemoteRunModulesRoot = %q", workspace.RemoteRunModulesRoot())
	}
}

func TestRunWorkspaceRejectsUnsafeScenarioResultPath(t *testing.T) {
	_, err := newRunWorkspace(t.TempDir(), "run-1", "C:/maya-stall", `..\secret.json`)
	if err == nil {
		t.Fatal("newRunWorkspace returned nil error for unsafe Scenario Result path")
	}
}
