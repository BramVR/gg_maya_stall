package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeBrokerCapturesRecordProvenanceOriginHashAndEvents(t *testing.T) {
	cases := []struct {
		name           string
		command        string
		kind           string
		content        []byte
		requestedEvent string
		capturedEvent  string
	}{
		{
			name:           "screenshot",
			command:        "screenshot",
			kind:           "screenshot",
			content:        []byte("fake screenshot\n"),
			requestedEvent: "broker.screenshot.capture-requested",
			capturedEvent:  "broker.screenshot.captured",
		},
		{
			name:           "recording",
			command:        "record",
			kind:           "recording",
			content:        []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 'f', 'a', 'k', 'e', '\n'},
			requestedEvent: "broker.recording.capture-requested",
			capturedEvent:  "broker.recording.captured",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeRunConfigFixture(t)
			var stdout, stderr bytes.Buffer

			code := Run([]string{tt.command}, &stdout, &stderr, dir, "test-version")
			if code != 0 {
				t.Fatalf("%s exit code = %d, want 0; stdout: %s stderr: %s", tt.command, code, stdout.String(), stderr.String())
			}

			evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
			bundle := readEvidenceBundle(t, evidence)
			if len(bundle.VisualEvidence) != 1 {
				t.Fatalf("visual evidence count = %d, want 1", len(bundle.VisualEvidence))
			}
			artifact := bundle.VisualEvidence[0]
			if artifact.Origin != visualEvidenceOriginFakeBrokerCapture {
				t.Fatalf("fake capture origin = %q, want %q", artifact.Origin, visualEvidenceOriginFakeBrokerCapture)
			}
			wantHash := sha256HexOfBytes(tt.content)
			if artifact.SHA256 != wantHash {
				t.Fatalf("fake capture sha256 = %q, want %q", artifact.SHA256, wantHash)
			}

			catalogHasProvenance := false
			for _, entry := range bundle.Artifacts {
				if entry.Label == "Visual Evidence" && entry.Path == artifact.Path {
					if entry.Origin != artifact.Origin || entry.SHA256 != artifact.SHA256 {
						t.Fatalf("catalog provenance = %+v, want origin %q sha256 %q", entry, artifact.Origin, artifact.SHA256)
					}
					catalogHasProvenance = true
				}
			}
			if !catalogHasProvenance {
				t.Fatalf("Evidence Bundle catalog missing Visual Evidence provenance entry: %+v", bundle.Artifacts)
			}

			events := readEventRecords(t, filepath.Join(evidence, evidenceEventsFileName))
			requested := eventRecordByName(t, events, tt.requestedEvent)
			if requested["origin"] != visualEvidenceOriginFakeBrokerCapture {
				t.Fatalf("capture-requested event = %+v, want origin %q", requested, visualEvidenceOriginFakeBrokerCapture)
			}
			captured := eventRecordByName(t, events, tt.capturedEvent)
			if captured["detail"] != artifact.Path || captured["origin"] != visualEvidenceOriginFakeBrokerCapture || captured["sha256"] != wantHash {
				t.Fatalf("captured event = %+v, want path %q origin %q sha256 %q", captured, artifact.Path, visualEvidenceOriginFakeBrokerCapture, wantHash)
			}
		})
	}
}

func TestDiscoverVisualEvidenceTagsDiscoveredOriginAndHash(t *testing.T) {
	evidence := t.TempDir()
	content := []byte("scenario dropped this file\n")
	mustWriteFile(t, filepath.Join(evidence, "screenshots", "extra.png"), string(content))

	artifacts, err := discoverVisualEvidence(evidence)
	if err != nil {
		t.Fatalf("discover Visual Evidence: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("discovered artifacts = %+v, want 1", artifacts)
	}
	got := artifacts[0]
	if got.Origin != visualEvidenceOriginDiscovered {
		t.Fatalf("discovered origin = %q, want %q", got.Origin, visualEvidenceOriginDiscovered)
	}
	if got.SHA256 != sha256HexOfBytes(content) {
		t.Fatalf("discovered sha256 = %q, want %q", got.SHA256, sha256HexOfBytes(content))
	}
}

func TestGGMayaSessiondFailedCaptureRecordsRequestedProvenanceEvent(t *testing.T) {
	dir := t.TempDir()
	failingSSH := filepath.Join(dir, "failing-ssh")
	mustWriteFile(t, failingSSH, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(failingSSH, 0o755); err != nil {
		t.Fatalf("make failing SSH executable: %v", err)
	}
	context := runContext{
		EvidenceDir: filepath.Join(dir, "evidence"),
		EventsPath:  filepath.Join(dir, "events.jsonl"),
		RunWorkspace: runWorkspace{
			runID:          "run-1",
			remoteWorkRoot: "C:/maya-stall",
		},
	}
	broker := ggMayaSessiondBroker{host: mayaHostConfig{
		Transport: "ssh",
		SSH:       sshConfig{Binary: failingSSH, Host: "maya-win-01"},
		WorkRoot:  "C:/maya-stall",
		Broker: brokerConfig{
			Type:     "gg-mayasessiond",
			StateDir: "C:/maya-stall/sessiond-ui",
			Python:   "C:/python.exe",
			Repo:     "C:/GG_MayaSessiond",
		},
	}}

	_, err := broker.CaptureScreenshot(context, screenshotRequest{Name: "failed.png"})
	if err == nil {
		t.Fatal("CaptureScreenshot succeeded with failing SSH transport")
	}
	events := readEventRecords(t, context.EventsPath)
	requested := eventRecordByName(t, events, "broker.screenshot.capture-requested")
	if requested["detail"] != "screenshots/failed.png" || requested["origin"] != visualEvidenceOriginBrokerCapture {
		t.Fatalf("capture-requested event = %+v", requested)
	}
	for _, event := range events {
		if event["event"] == "broker.screenshot.captured" {
			t.Fatalf("failed capture appended captured event: %+v", event)
		}
	}
}

func TestRequireBrokerCaptureProvenanceEvents(t *testing.T) {
	artifact := visualEvidenceArtifact{
		Kind:   "screenshot",
		Path:   "screenshots/desktop.png",
		Origin: visualEvidenceOriginBrokerCapture,
		SHA256: sha256HexOfBytes([]byte("desktop")),
	}
	cases := []struct {
		name    string
		events  string
		wantErr string
	}{
		{
			name: "matching ordered events",
			events: fmt.Sprintf("{\"event\":\"broker.screenshot.capture-requested\",\"detail\":%q,\"origin\":%q}\n{\"event\":\"broker.screenshot.captured\",\"detail\":%q,\"origin\":%q,\"sha256\":%q}\n",
				artifact.Path, artifact.Origin, artifact.Path, artifact.Origin, artifact.SHA256),
		},
		{
			name: "large unrelated event detail",
			events: fmt.Sprintf("{\"event\":\"run.failed\",\"detail\":%q}\n{\"event\":\"broker.screenshot.capture-requested\",\"detail\":%q,\"origin\":%q}\n{\"event\":\"broker.screenshot.captured\",\"detail\":%q,\"origin\":%q,\"sha256\":%q}\n",
				strings.Repeat("x", 70*1024), artifact.Path, artifact.Origin, artifact.Path, artifact.Origin, artifact.SHA256),
		},
		{
			name: "missing request",
			events: fmt.Sprintf("{\"event\":\"broker.screenshot.captured\",\"detail\":%q,\"origin\":%q,\"sha256\":%q}\n",
				artifact.Path, artifact.Origin, artifact.SHA256),
			wantErr: "missing capture-requested",
		},
		{
			name: "captured hash mismatch",
			events: fmt.Sprintf("{\"event\":\"broker.screenshot.capture-requested\",\"detail\":%q,\"origin\":%q}\n{\"event\":\"broker.screenshot.captured\",\"detail\":%q,\"origin\":%q,\"sha256\":\"wrong\"}\n",
				artifact.Path, artifact.Origin, artifact.Path, artifact.Origin),
			wantErr: "missing matching captured",
		},
		{
			name: "captured precedes request",
			events: fmt.Sprintf("{\"event\":\"broker.screenshot.captured\",\"detail\":%q,\"origin\":%q,\"sha256\":%q}\n{\"event\":\"broker.screenshot.capture-requested\",\"detail\":%q,\"origin\":%q}\n",
				artifact.Path, artifact.Origin, artifact.SHA256, artifact.Path, artifact.Origin),
			wantErr: "before matching capture-requested",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, evidenceEventsFileName), tt.events)
			err := requireBrokerCaptureProvenanceEvents(dir, evidenceBundle{
				Events:         evidenceEventsFileName,
				VisualEvidence: []visualEvidenceArtifact{artifact},
			})
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("require provenance events: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("require provenance events error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRejectNonBrokerVisualEvidenceForLiveProof(t *testing.T) {
	liveRuntime := runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true}
	fakeRuntime := runtimeMetadata{Profile: "fake-local", HostAdapter: "fake", BrokerAdapter: "fake", LiveProofEligible: false}
	cases := []struct {
		name    string
		runtime runtimeMetadata
		origin  string
		wantErr string
	}{
		{name: "live accepts broker capture", runtime: liveRuntime, origin: visualEvidenceOriginBrokerCapture},
		{name: "live rejects discovered", runtime: liveRuntime, origin: visualEvidenceOriginDiscovered, wantErr: `origin "discovered"`},
		{name: "live rejects fake broker capture", runtime: liveRuntime, origin: visualEvidenceOriginFakeBrokerCapture, wantErr: `origin "fake-broker-capture"`},
		{name: "live rejects missing origin", runtime: liveRuntime, origin: "", wantErr: `origin "unknown"`},
		{name: "fake runtime accepts fake broker capture", runtime: fakeRuntime, origin: visualEvidenceOriginFakeBrokerCapture},
		{name: "fake runtime accepts discovered", runtime: fakeRuntime, origin: visualEvidenceOriginDiscovered},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			artifacts := []visualEvidenceArtifact{{
				Kind:      "screenshot",
				Path:      "screenshots/screenshot.png",
				MediaType: "image/png",
				Origin:    tt.origin,
			}}
			err := rejectNonBrokerVisualEvidenceForLiveProof(tt.runtime, artifacts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("rejectNonBrokerVisualEvidenceForLiveProof returned error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("rejectNonBrokerVisualEvidenceForLiveProof error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestEvidencePublishCarriesVisualEvidenceProvenance(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, ".maya-stall.yaml"), `version: 1
scenarios:
  smoke:
    payload: {}
    expectedOutputs:
      scenarioResult: "outputs/result.json"
    evidence:
      screenshots:
        enabled: true
`)
	var stdout, stderr bytes.Buffer

	code := Run([]string{"evidence", "collect", "smoke"}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence collect exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidence := onlyRunDir(t, filepath.Join(dir, "artifacts", "maya-stall"))
	bundle := readEvidenceBundle(t, evidence)
	if len(bundle.VisualEvidence) != 1 || bundle.VisualEvidence[0].Origin == "" || bundle.VisualEvidence[0].SHA256 == "" {
		t.Fatalf("collected Visual Evidence provenance = %+v", bundle.VisualEvidence)
	}
	store := filepath.Join(t.TempDir(), "store")

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"evidence", "publish", "--destination", store, "--base-url", "https://evidence.example.test/maya", evidence}, &stdout, &stderr, dir, "test-version")
	if code != 0 {
		t.Fatalf("evidence publish exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}

	var manifest publishedArtifactManifest
	readJSONFile(t, filepath.Join(store, bundle.RunID, evidencePublishedManifestName), &manifest)
	found := false
	for _, artifact := range manifest.Artifacts {
		if artifact.Label != "Visual Evidence" {
			continue
		}
		found = true
		if artifact.Path != bundle.VisualEvidence[0].Path {
			t.Fatalf("published Visual Evidence path = %q, want %q", artifact.Path, bundle.VisualEvidence[0].Path)
		}
		if artifact.Origin != bundle.VisualEvidence[0].Origin || artifact.SHA256 != bundle.VisualEvidence[0].SHA256 {
			t.Fatalf("published Visual Evidence provenance = %+v, want origin %q sha256 %q", artifact, bundle.VisualEvidence[0].Origin, bundle.VisualEvidence[0].SHA256)
		}
	}
	if !found {
		t.Fatalf("published artifact manifest missing Visual Evidence entry: %+v", manifest.Artifacts)
	}
}

func readEventRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read events %s: %v", path, err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("parse event line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func eventRecordByName(t *testing.T, records []map[string]any, event string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["event"] == event {
			return record
		}
	}
	t.Fatalf("events missing %q: %+v", event, records)
	return nil
}
