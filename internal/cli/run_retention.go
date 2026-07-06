package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

type brokerCapabilities struct {
	RetainOnFailure          bool `json:"retainOnFailure"`
	StatusRetainedSession    bool `json:"statusRetainedSession"`
	AttachLogObservation     bool `json:"attachLogObservation"`
	StopRetainedSession      bool `json:"stopRetainedSession"`
	CleanupRetainedWorkspace bool `json:"cleanupRetainedWorkspace"`
}

type retainedSessionRecord struct {
	BrokerAdapter string         `json:"brokerAdapter"`
	SessionID     string         `json:"sessionId,omitempty"`
	Status        string         `json:"status,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type retainedRunStatus struct {
	State           string `json:"state"`
	Detail          string `json:"detail,omitempty"`
	BrokerStatus    string `json:"brokerStatus,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	RemoteWorkspace string `json:"remoteWorkspace,omitempty"`
}

type runRetentionRecord struct {
	RunID               string                `json:"runId"`
	Scenario            string                `json:"scenario"`
	TargetProfile       string                `json:"targetProfile"`
	Host                string                `json:"host"`
	Runtime             runtimeMetadata       `json:"runtime"`
	Status              string                `json:"status"`
	RetentionReason     string                `json:"retentionReason,omitempty"`
	LocalStateDir       string                `json:"localStateDir"`
	LocalEvidenceDir    string                `json:"localEvidenceDir"`
	LocalWorkspace      string                `json:"localWorkspace"`
	ScenarioResultPath  string                `json:"scenarioResultPath,omitempty"`
	RemoteRunRoot       string                `json:"remoteRunRoot,omitempty"`
	RemoteWorkspace     string                `json:"remoteWorkspace,omitempty"`
	HostConfig          mayaHostConfig        `json:"hostConfig"`
	BrokerCapabilities  brokerCapabilities    `json:"brokerCapabilities"`
	RemoteSession       retainedSessionRecord `json:"remoteSession"`
	CreatedAt           string                `json:"createdAt"`
	UpdatedAt           string                `json:"updatedAt"`
	LegacyMissingRecord bool                  `json:"-"`
}

func newRunRetentionRecord(context runContext, manifest runManifest, host mayaHostConfig, status string, reason string) runRetentionRecord {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return runRetentionRecord{
		RunID:              manifest.RunID,
		Scenario:           manifest.Scenario,
		TargetProfile:      manifest.TargetProfile,
		Host:               manifest.Host,
		Runtime:            manifest.Runtime,
		Status:             status,
		RetentionReason:    reason,
		LocalStateDir:      context.StateDir,
		LocalEvidenceDir:   context.EvidenceDir,
		LocalWorkspace:     context.Workspace,
		ScenarioResultPath: context.ScenarioResultPath,
		RemoteRunRoot:      context.RunWorkspace.RemoteRunRoot(),
		RemoteWorkspace:    context.RunWorkspace.RemoteWorkspace(),
		HostConfig:         host,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func writeRunRetentionRecord(context runContext, record runRetentionRecord) error {
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONFile(filepath.Join(context.StateDir, "run-record.json"), record)
}

func fallbackRunRetentionRecord(repoDir string, stateDir string, manifest runManifest) runRetentionRecord {
	workspace, _ := newRunWorkspace(repoDir, manifest.RunID, "", "")
	return runRetentionRecord{
		RunID:            manifest.RunID,
		Scenario:         manifest.Scenario,
		TargetProfile:    manifest.TargetProfile,
		Host:             manifest.Host,
		Runtime:          manifest.Runtime,
		Status:           "kept",
		LocalStateDir:    stateDir,
		LocalEvidenceDir: filepath.Join(repoDir, "artifacts", "maya-stall", manifest.RunID),
		LocalWorkspace:   filepath.Join(stateDir, "workspace"),
		HostConfig:       mayaHostConfig{ID: manifest.Host},
		RemoteSession: retainedSessionRecord{
			BrokerAdapter: manifest.Runtime.BrokerAdapter,
			Status:        "unknown",
		},
		LegacyMissingRecord: true,
		BrokerCapabilities: brokerCapabilities{
			RetainOnFailure:          manifest.Runtime.BrokerAdapter == "fake",
			StatusRetainedSession:    manifest.Runtime.BrokerAdapter == "fake",
			AttachLogObservation:     manifest.Runtime.BrokerAdapter == "fake",
			StopRetainedSession:      manifest.Runtime.BrokerAdapter == "fake",
			CleanupRetainedWorkspace: manifest.Runtime.BrokerAdapter == "fake",
		},
		RemoteRunRoot:   workspace.RemoteRunRoot(),
		RemoteWorkspace: workspace.RemoteWorkspace(),
	}
}

func retentionBrokerForRecord(record runRetentionRecord) (runRetentionBroker, error) {
	switch record.Runtime.BrokerAdapter {
	case "", "fake":
		return fakeSessionBroker{}, nil
	case "gg-mayasessiond":
		return ggMayaSessiondBroker{host: record.HostConfig}, nil
	default:
		return nil, unsupportedBrokerCapabilityError(record.Runtime.BrokerAdapter, "run-retention")
	}
}

func unsupportedBrokerCapabilityError(adapter string, capability string) error {
	if adapter == "" {
		adapter = "unknown"
	}
	return fmt.Errorf("Session Broker %q does not support %s for kept sessions; see docs/adr/0033-manage-run-retention-on-owned-hosts.md and docs/setup/windows-maya-host.md#host-lock-and-retention for cleanup guidance", adapter, capability)
}

func requireRetentionCapability(broker runRetentionBroker, adapter string, capability string, supported bool) error {
	if broker == nil || !supported {
		return unsupportedBrokerCapabilityError(adapter, capability)
	}
	return nil
}

func attachLocalRunFiles(run keptRun, stdout io.Writer) error {
	fmt.Fprintln(stdout, "events:")
	if err := copyRunStateTextFile(run.StateDir, "events.jsonl", stdout); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "logs:")
	if err := copyRunStateTextFile(run.StateDir, filepath.Join("logs", "session.log"), stdout); err != nil {
		return err
	}
	if run.Record.ScenarioResultPath != "" {
		relativeResult, err := filepath.Rel(run.StateDir, run.Record.ScenarioResultPath)
		if err != nil || filepath.IsAbs(relativeResult) || strings.HasPrefix(filepath.ToSlash(relativeResult), "../") || filepath.ToSlash(relativeResult) == ".." || !strings.HasPrefix(filepath.ToSlash(relativeResult), "workspace/") {
			return fmt.Errorf("Scenario Result path for run %s must stay under kept run workspace", run.RunID)
		}
		fmt.Fprintln(stdout, "scenarioResult:")
		if err := copyRunStateTextFile(run.StateDir, relativeResult, stdout); err != nil {
			return err
		}
	}
	if run.Record.LocalEvidenceDir != "" {
		fmt.Fprintf(stdout, "evidence: %s\n", run.Record.LocalEvidenceDir)
	}
	return nil
}
