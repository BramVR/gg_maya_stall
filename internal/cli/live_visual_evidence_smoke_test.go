package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const liveVisualEvidenceSmokeDuration = 2 * time.Second

func TestOptInRealVisualEvidenceSmoke(t *testing.T) {
	options, ok := realSSHSmokeOptionsFromEnv(t)
	if !ok {
		return
	}
	dir := writeLiveRunConfigFixture(t)

	doctorOptions := options.doctorOptions()
	doctorOptions.ScenarioName = "smoke"
	report := runDoctor(dir, doctorOptions)
	assertLiveVisualEvidenceHostProof(t, report)
	t.Logf("Host Health: %s", formatHostHealthReport(report))

	evidenceDir := captureLiveDesktopVisualEvidenceProof(t, dir, options)
	bundle := assertLiveVisualEvidenceProofBundle(t, evidenceDir)
	screenshot, recording := requireLiveDesktopVisualArtifacts(t, evidenceDir, bundle)
	t.Logf("Live Visual Evidence proof: evidence=%s screenshot=%s bytes=%d recording=%s bytes=%d",
		evidenceDir,
		filepath.Join(evidenceDir, filepath.FromSlash(screenshot.Path)),
		artifactSize(t, evidenceDir, screenshot),
		filepath.Join(evidenceDir, filepath.FromSlash(recording.Path)),
		artifactSize(t, evidenceDir, recording),
	)
}

func TestLiveVisualEvidenceProofRejectsInvalidProofShapes(t *testing.T) {
	liveRuntime := runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true}
	console := []windowsProcessSession{{ProcessID: 42, SessionID: 1, SessionName: "Console", Name: "maya.exe"}}
	cases := []struct {
		name      string
		runtime   runtimeMetadata
		processes []windowsProcessSession
		visual    []visualEvidenceArtifact
		files     map[string][]byte
		wantErr   string
		wantValid bool
	}{
		{
			name:      "valid desktop screenshot and recording",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png"},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4"},
			},
			files: map[string][]byte{
				"screenshots/desktop-screenshot.png": pngHeaderBytes(),
				"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
			},
			wantValid: true,
		},
		{
			name:      "fake runtime",
			runtime:   runtimeMetadata{Profile: "fake-local", HostAdapter: "fake", BrokerAdapter: "fake"},
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png"},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4"},
			},
			files: map[string][]byte{
				"screenshots/desktop-screenshot.png": pngHeaderBytes(),
				"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
			},
			wantErr: "live-proof-eligible ssh-sessiond",
		},
		{
			name:      "viewport screenshot only",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/smoke.jpg", MediaType: "image/jpeg"},
			},
			files:   map[string][]byte{"screenshots/smoke.jpg": jpegHeaderBytes()},
			wantErr: "desktop screenshot and desktop recording",
		},
		{
			name:      "non console maya",
			runtime:   liveRuntime,
			processes: []windowsProcessSession{{ProcessID: 42, SessionID: 0, SessionName: "Services", Name: "maya.exe"}},
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png"},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4"},
			},
			files: map[string][]byte{
				"screenshots/desktop-screenshot.png": pngHeaderBytes(),
				"recordings/desktop-recording.mp4":   mp4HeaderBytes(),
			},
			wantErr: "interactive Console session",
		},
		{
			name:      "fake bytes",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "screenshot", Path: "screenshots/desktop-screenshot.png", MediaType: "image/png"},
				{Kind: "recording", Path: "recordings/desktop-recording.mp4", MediaType: "video/mp4"},
			},
			files: map[string][]byte{
				"screenshots/desktop-screenshot.png": []byte("fake screenshot"),
				"recordings/desktop-recording.mp4":   []byte("fake recording"),
			},
			wantErr: "does not look like a PNG",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			evidenceDir := writeLiveVisualEvidenceProofBundle(t, tt.runtime, tt.visual, tt.files)
			bundle, err := validateLiveVisualEvidenceProofBundle(evidenceDir, tt.processes)
			if tt.wantValid {
				if err != nil {
					t.Fatalf("validateLiveVisualEvidenceProofBundle returned error: %v", err)
				}
				if len(bundle.VisualEvidence) != 2 {
					t.Fatalf("Visual Evidence count = %d, want 2", len(bundle.VisualEvidence))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateLiveVisualEvidenceProofBundle error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLiveVisualEvidenceProofWorkflowRequiresSmokePass(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "proof.yml"))
	if err != nil {
		t.Fatalf("read proof workflow: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"TestOptInRealVisualEvidenceSmoke",
		"run TestOptInRealVisualEvidenceSmoke -count=1",
		"run 'TestOptInRealSSH(Doctor|Run|ConsumingRepo)Smoke' -count=1",
		"failed_missing_host_config",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("proof workflow missing %q", want)
		}
	}
}

func TestLiveVisualEvidenceHostProofDoesNotDependOnViewportCapture(t *testing.T) {
	report := hostHealthReport{
		Runtime: runtimeMetadata{Profile: "ssh-sessiond", HostAdapter: "ssh", BrokerAdapter: "gg-mayasessiond", LiveProofEligible: true},
		Layers: []hostHealthLayer{
			okCheck("local-config", ".maya-stall.yaml"),
			okCheck("scenario-inputs", "smoke"),
			okCheck("target-profile", "default"),
			okCheck("host-pool", "windows-maya"),
			okCheck("host", "maya-win-01"),
			okCheck("ssh", "reachable"),
			okCheck("work-root", "writable"),
			withState(okCheck("host-lock", "unlocked"), "unlocked"),
			withSource(okCheck("session-broker", "gg_mayasessiond reachable; Maya UI is interactive"), "gg-mayasessiond"),
			okCheck("maya-version", "2025 satisfies Scenario smoke"),
			withSource(failedCheck("visual-evidence", "Error calling tool 'viewport.capture': Maya did not return an output path.", "Repair viewport.capture in gg_mayasessiond."), "session-broker"),
		},
	}
	report.Layers[8].InteractiveDesktop = true
	if err := validateLiveVisualEvidenceHostProof(report); err != nil {
		t.Fatalf("validateLiveVisualEvidenceHostProof returned error: %v", err)
	}

	report.Layers[8].InteractiveDesktop = false
	if err := validateLiveVisualEvidenceHostProof(report); err == nil || !strings.Contains(err.Error(), "interactive gg_mayasessiond") {
		t.Fatalf("validateLiveVisualEvidenceHostProof error = %v, want interactive broker failure", err)
	}
}

func TestWindowsDesktopCaptureCommandsUseInteractiveDesktop(t *testing.T) {
	screenshot := windowsDesktopScreenshotPowerShell("C:/maya-stall/artifacts/proof")
	for _, want := range []string{"System.Windows.Forms", "ImageFormat]::Png", "schtasks.exe", "/IT", "MayaStallVisualEvidenceScreenshot"} {
		if !strings.Contains(screenshot, want) {
			t.Fatalf("screenshot command missing %q:\n%s", want, screenshot)
		}
	}
	if strings.Contains(screenshot, "viewport.capture") {
		t.Fatalf("screenshot command must not use viewport.capture:\n%s", screenshot)
	}

	recording := windowsDesktopRecordingPowerShell("C:/maya-stall/artifacts/proof", 3, 500)
	for _, want := range []string{"System.Windows.Forms", "ImageFormat]::Jpeg", "Compress-Archive", "frame-*.jpg", "schtasks.exe", "/IT", "MayaStallVisualEvidenceRecording"} {
		if !strings.Contains(recording, want) {
			t.Fatalf("recording command missing %q:\n%s", want, recording)
		}
	}
	if strings.Contains(recording, "viewport.capture") {
		t.Fatalf("recording command must not use viewport.capture:\n%s", recording)
	}
}

func captureLiveDesktopVisualEvidenceProof(t *testing.T, repoDir string, options realSSHSmokeOptions) string {
	t.Helper()
	host, err := selectHostForRun(repoDir, runOptions{
		HostConfig:    options.HostConfig,
		TargetProfile: options.TargetProfile,
		HostPin:       options.Host,
	})
	if err != nil {
		t.Fatalf("select live Maya Host: %v", err)
	}
	defer func() {
		if host.release != nil {
			if err := host.release(); err != nil {
				t.Fatalf("release Host Lock for %s: %v", host.HostID, err)
			}
		}
	}()
	resolved, err := resolveRuntimeForHost(host.Config)
	if err != nil {
		t.Fatalf("resolve live runtime: %v", err)
	}
	if err := requireLiveRuntime(resolved.Metadata); err != nil {
		t.Fatal(err)
	}
	processes, err := mayaTasklistSessions(host.Config)
	if err != nil {
		t.Fatalf("query maya.exe tasklist sessions: %v", err)
	}
	if err := requireConsoleMayaProcess(processes); err != nil {
		t.Fatal(err)
	}

	runID := time.Now().UTC().Format("20060102T150405.000000000Z")
	workspace, err := newRunWorkspace(repoDir, runID, host.Config.WorkRoot, evidenceStandaloneResultName)
	if err != nil {
		t.Fatalf("create live Visual Evidence workspace: %v", err)
	}
	context := runContext{
		RepoDir:      repoDir,
		RunWorkspace: workspace,
		StateDir:     workspace.StateDir(),
		EvidenceDir:  workspace.EvidenceDir(),
		Workspace:    workspace.LocalWorkspace(),
		EventsPath:   workspace.EventsPath(),
		LogPath:      workspace.LogPath(),
	}
	if err := createCleanRunDirs(context); err != nil {
		t.Fatalf("create live Visual Evidence dirs: %v", err)
	}
	manifest := runManifest{
		RunID:         runID,
		Scenario:      "live-visual-evidence-proof",
		TargetProfile: host.TargetProfile,
		Host:          host.HostID,
		Runtime:       resolved.Metadata,
	}
	if err := writeJSONFile(filepath.Join(context.StateDir, "manifest.json"), manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := appendEvent(context.EventsPath, "visual-evidence.live-proof.started", "desktop"); err != nil {
		t.Fatalf("write start event: %v", err)
	}
	if err := os.WriteFile(context.LogPath, []byte("live desktop Visual Evidence proof captured from the interactive Windows session\n"), 0o644); err != nil {
		t.Fatalf("write proof log: %v", err)
	}

	remoteRoot := remoteJoin(host.Config.WorkRoot, "artifacts", "live-visual-evidence-"+runID)
	transport := sshWindowsDesktopTransport(host.Config)
	screenshotBytes, err := captureWindowsDesktopScreenshot(transport, remoteRoot)
	if err != nil {
		t.Fatalf("capture desktop screenshot: %v", err)
	}
	screenshot, err := registerVisualEvidenceBytes(context, "screenshot", "desktop-screenshot.png", "image/png", screenshotBytes)
	if err != nil {
		t.Fatalf("register desktop screenshot: %v", err)
	}
	recordingBytes, err := captureWindowsDesktopRecording(transport, remoteRoot, liveVisualEvidenceSmokeDuration, 2, "")
	if err != nil {
		t.Fatalf("capture desktop recording: %v", err)
	}
	recording, err := registerVisualEvidenceBytes(context, "recording", "desktop-recording.mp4", "video/mp4", recordingBytes)
	if err != nil {
		t.Fatalf("register desktop recording: %v", err)
	}
	result := ScenarioResult{Status: resultStatusPassed, Summary: "live desktop Visual Evidence proof captured"}
	if err := writeJSONFile(filepath.Join(context.EvidenceDir, evidenceScenarioResultFileName), result); err != nil {
		t.Fatalf("write proof result: %v", err)
	}
	if err := appendEvent(context.EventsPath, "visual-evidence.live-proof.completed", screenshot.Path+" "+recording.Path); err != nil {
		t.Fatalf("write complete event: %v", err)
	}
	if err := writeEvidenceBundle(context, manifest, scenarioContract{}, result, []visualEvidenceArtifact{screenshot, recording}, nil); err != nil {
		t.Fatalf("write Evidence Bundle: %v", err)
	}
	if _, err := validateLiveVisualEvidenceProofBundle(context.EvidenceDir, processes); err != nil {
		t.Fatalf("validate live Visual Evidence proof: %v", err)
	}
	return context.EvidenceDir
}

func mayaTasklistSessions(host mayaHostConfig) ([]windowsProcessSession, error) {
	script := `$ErrorActionPreference = 'Stop'
$rows = @(tasklist.exe /v /fi "imagename eq maya.exe" /fo csv | ConvertFrom-Csv | Where-Object { $_.'Image Name' -ieq 'maya.exe' } | ForEach-Object {
  [pscustomobject]@{
    ProcessId = [int]$_.PID
    SessionId = [int]$_.'Session#'
    SessionName = $_.'Session Name'
    UserName = $_.'User Name'
    Name = $_.'Image Name'
  }
})
if ($rows.Count -eq 0) { Write-Output '[]' } else { $rows | ConvertTo-Json -Compress }`
	raw, err := runSSHCommandOutput(host, encodedPowerShellCommand(script), sessiondCommandTimeout)
	if err != nil {
		return nil, err
	}
	return parseWindowsProcessSessions(raw)
}

func parseWindowsProcessSessions(raw []byte) ([]windowsProcessSession, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var processes []windowsProcessSession
		if err := json.Unmarshal([]byte(trimmed), &processes); err != nil {
			return nil, fmt.Errorf("parse maya.exe process JSON: %w", err)
		}
		return processes, nil
	}
	var process windowsProcessSession
	if err := json.Unmarshal([]byte(trimmed), &process); err != nil {
		return nil, fmt.Errorf("parse maya.exe process JSON: %w", err)
	}
	return []windowsProcessSession{process}, nil
}

func assertLiveVisualEvidenceHostProof(t *testing.T, report hostHealthReport) {
	t.Helper()
	if err := validateLiveVisualEvidenceHostProof(report); err != nil {
		t.Fatalf("Host Health is not ready for live desktop Visual Evidence proof: %v: %s", err, formatHostHealthReport(report))
	}
	if visual, ok := hostHealthLayerByID(report, "visual-evidence"); ok && visual.Status != "ok" {
		t.Logf("viewport Visual Evidence health is not used as live desktop proof: %+v", visual)
	}
}

func validateLiveVisualEvidenceHostProof(report hostHealthReport) error {
	if err := requireLiveRuntime(report.Runtime); err != nil {
		return err
	}
	for _, id := range []string{"local-config", "scenario-inputs", "target-profile", "host-pool", "host", "ssh", "work-root", "host-lock", "maya-version"} {
		layer, ok := hostHealthLayerByID(report, id)
		if !ok {
			return fmt.Errorf("Host Health missing %s layer", id)
		}
		if layer.Status != "ok" {
			return fmt.Errorf("Host Health %s layer = %+v, want ok", id, layer)
		}
	}
	broker, ok := hostHealthLayerByID(report, "session-broker")
	if !ok {
		return fmt.Errorf("Host Health missing session-broker layer")
	}
	if broker.Status != "ok" || broker.Source != "gg-mayasessiond" || !broker.InteractiveDesktop {
		return fmt.Errorf("session-broker Host Health = %+v, want interactive gg_mayasessiond", broker)
	}
	return nil
}

func hostHealthLayerByID(report hostHealthReport, id string) (hostHealthLayer, bool) {
	for _, layer := range report.Layers {
		if layer.ID == id {
			return layer, true
		}
	}
	return hostHealthLayer{}, false
}

func assertLiveVisualEvidenceProofBundle(t *testing.T, evidenceDir string) evidenceBundle {
	t.Helper()
	processes := []windowsProcessSession{{ProcessID: 1, SessionID: 1, SessionName: "Console", Name: "maya.exe"}}
	bundle, err := validateLiveVisualEvidenceProofBundle(evidenceDir, processes)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func validateLiveVisualEvidenceProofBundle(evidenceDir string, processes []windowsProcessSession) (evidenceBundle, error) {
	content, err := os.ReadFile(filepath.Join(evidenceDir, evidenceBundleFileName))
	if err != nil {
		return evidenceBundle{}, err
	}
	var bundle evidenceBundle
	if err := json.Unmarshal(content, &bundle); err != nil {
		return evidenceBundle{}, err
	}
	if err := requireLiveRuntime(bundle.Runtime); err != nil {
		return evidenceBundle{}, err
	}
	if err := requireConsoleMayaProcess(processes); err != nil {
		return evidenceBundle{}, err
	}
	screenshot, recording, err := liveDesktopVisualArtifacts(bundle)
	if err != nil {
		return evidenceBundle{}, err
	}
	screenshotBytes, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(screenshot.Path)))
	if err != nil {
		return evidenceBundle{}, err
	}
	if !looksLikeImageBytes("image/png", screenshotBytes) {
		return evidenceBundle{}, fmt.Errorf("desktop screenshot %s does not look like a PNG", screenshot.Path)
	}
	recordingBytes, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(recording.Path)))
	if err != nil {
		return evidenceBundle{}, err
	}
	if !looksLikeMP4Bytes(recordingBytes) {
		return evidenceBundle{}, fmt.Errorf("desktop recording %s does not look like an MP4", recording.Path)
	}
	return bundle, nil
}

func requireLiveRuntime(runtime runtimeMetadata) error {
	if runtime.Profile != "ssh-sessiond" || runtime.HostAdapter != "ssh" || runtime.BrokerAdapter != "gg-mayasessiond" || !runtime.LiveProofEligible {
		return fmt.Errorf("Visual Evidence proof runtime = %+v, want live-proof-eligible ssh-sessiond", runtime)
	}
	return nil
}

func requireConsoleMayaProcess(processes []windowsProcessSession) error {
	if len(processes) == 0 {
		return fmt.Errorf("maya.exe is not running in the interactive Console session")
	}
	for _, process := range processes {
		if strings.EqualFold(process.Name, "maya.exe") && strings.EqualFold(process.SessionName, "Console") && process.SessionID != 0 {
			return nil
		}
	}
	return fmt.Errorf("maya.exe is not running in the interactive Console session: %+v", processes)
}

func requireLiveDesktopVisualArtifacts(t *testing.T, evidenceDir string, bundle evidenceBundle) (visualEvidenceArtifact, visualEvidenceArtifact) {
	t.Helper()
	screenshot, recording, err := liveDesktopVisualArtifacts(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []visualEvidenceArtifact{screenshot, recording} {
		if _, err := os.Stat(filepath.Join(evidenceDir, filepath.FromSlash(artifact.Path))); err != nil {
			t.Fatalf("missing Visual Evidence artifact %s: %v", artifact.Path, err)
		}
	}
	return screenshot, recording
}

func liveDesktopVisualArtifacts(bundle evidenceBundle) (visualEvidenceArtifact, visualEvidenceArtifact, error) {
	var screenshot visualEvidenceArtifact
	var recording visualEvidenceArtifact
	for _, artifact := range bundle.VisualEvidence {
		switch {
		case artifact.Kind == "screenshot" && artifact.MediaType == "image/png" && artifact.Path == "screenshots/desktop-screenshot.png":
			screenshot = artifact
		case artifact.Kind == "recording" && artifact.MediaType == "video/mp4" && artifact.Path == "recordings/desktop-recording.mp4":
			recording = artifact
		}
	}
	if screenshot.Path == "" || recording.Path == "" {
		return visualEvidenceArtifact{}, visualEvidenceArtifact{}, fmt.Errorf("live Visual Evidence proof requires desktop screenshot and desktop recording artifacts, got %+v", bundle.VisualEvidence)
	}
	return screenshot, recording, nil
}

func looksLikeMP4Bytes(content []byte) bool {
	return len(content) >= 12 && string(content[4:8]) == "ftyp"
}

func artifactSize(t *testing.T, evidenceDir string, artifact visualEvidenceArtifact) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(evidenceDir, filepath.FromSlash(artifact.Path)))
	if err != nil {
		t.Fatalf("stat %s: %v", artifact.Path, err)
	}
	return info.Size()
}

func writeLiveVisualEvidenceProofBundle(t *testing.T, runtime runtimeMetadata, visual []visualEvidenceArtifact, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, evidenceManifestFileName), "{}\n")
	mustWriteFile(t, filepath.Join(dir, evidenceEventsFileName), "{}\n")
	mustWriteFile(t, filepath.Join(dir, evidenceLogPath), "log\n")
	mustWriteFile(t, filepath.Join(dir, evidenceScenarioResultFileName), `{"status":"passed"}`+"\n")
	for relative, content := range files {
		mustWriteFile(t, filepath.Join(dir, filepath.FromSlash(relative)), string(content))
	}
	bundle := evidenceBundle{
		RunID:          "20260706T120000.000000000Z",
		Scenario:       "live-visual-evidence-proof",
		Status:         resultStatusPassed,
		TargetProfile:  "ci",
		Host:           "alpha",
		Runtime:        runtime,
		Manifest:       evidenceManifestFileName,
		Events:         evidenceEventsFileName,
		Log:            evidenceLogPath,
		ScenarioResult: evidenceScenarioResultFileName,
		VisualEvidence: visual,
	}
	bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
	if err := writeJSONFile(filepath.Join(dir, evidenceBundleFileName), bundle); err != nil {
		t.Fatalf("write evidence bundle: %v", err)
	}
	return dir
}

func pngHeaderBytes() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0}
}

func jpegHeaderBytes() []byte {
	return []byte{0xff, 0xd8, 0xff, 0xdb}
}

func mp4HeaderBytes() []byte {
	return []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}
}
