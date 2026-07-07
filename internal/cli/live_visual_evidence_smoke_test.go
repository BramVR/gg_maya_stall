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

	evidenceDir := captureLiveRecordCommandProof(t, dir, options)
	bundle := assertLiveRecordCommandProofBundle(t, evidenceDir, options)
	recording := requireLiveRecordCommandArtifact(t, evidenceDir, bundle)
	t.Logf("Live record command proof: run=%s recording=%s bytes=%d",
		bundle.RunID,
		recording.Path,
		artifactSize(t, evidenceDir, recording),
	)
	addLiveDesktopScreenshotForProofArtifact(t, dir, evidenceDir, options)
	bundle = assertLiveVisualEvidenceProofBundle(t, evidenceDir)
	screenshot, recording := requireLiveDesktopVisualArtifacts(t, evidenceDir, bundle)
	t.Logf("Live Visual Evidence proof artifact source: run=%s screenshot=%s bytes=%d recording=%s bytes=%d",
		bundle.RunID,
		screenshot.Path,
		artifactSize(t, evidenceDir, screenshot),
		recording.Path,
		artifactSize(t, evidenceDir, recording),
	)
	publishOptionalLiveVisualEvidenceProofArtifact(t, evidenceDir)
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
		"MAYA_STALL_LIVE_PROOF_ARTIFACT_ENABLED",
		"MAYA_STALL_LIVE_PROOF_MEDIA_REVIEWED",
		"live-visual-evidence-proof",
		"assert-public-artifact-confidentiality.mjs",
		"failed_missing_visual_evidence_proof_artifact",
		"failed_visual_evidence_proof_confidentiality",
		"failed_visual_evidence_proof_upload",
		"failed_missing_host_config",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("proof workflow missing %q", want)
		}
	}
}

func TestRealSSHSmokeRecordArgsUseStandaloneCommand(t *testing.T) {
	options := realSSHSmokeOptions{
		HostConfig:    "/tmp/hosts.yaml",
		TargetProfile: "ci",
		Host:          "maya-win-01",
	}
	got := options.recordArgs()
	want := []string{"record", "--host-config", "/tmp/hosts.yaml", "--target-profile", "ci", "--host", "maya-win-01"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("record args = %#v, want %#v", got, want)
	}
}

func TestLiveRecordCommandProofRejectsInvalidProofShapes(t *testing.T) {
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
			name:      "valid standalone record command bundle",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:     map[string][]byte{"recordings/recording.mp4": mp4HeaderBytes()},
			wantValid: true,
		},
		{
			name:      "missing recording metadata",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:   map[string][]byte{"recordings/recording.mp4": mp4HeaderBytes()},
			wantErr: "duration/FPS metadata",
		},
		{
			name:      "missing selected host metadata",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci"},
			},
			files:   map[string][]byte{"recordings/recording.mp4": mp4HeaderBytes()},
			wantErr: "selected Maya Host metadata",
		},
		{
			name:      "fake bytes",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:   map[string][]byte{"recordings/recording.mp4": []byte("fake recording")},
			wantErr: "does not look like an MP4",
		},
		{
			name:      "fake runtime",
			runtime:   runtimeMetadata{Profile: "fake-local", HostAdapter: "fake", BrokerAdapter: "fake"},
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:   map[string][]byte{"recordings/recording.mp4": mp4HeaderBytes()},
			wantErr: "live-proof-eligible ssh-sessiond",
		},
		{
			name:      "traversal recording path",
			runtime:   liveRuntime,
			processes: console,
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/../logs/session.log", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:   map[string][]byte{"logs/session.log": mp4HeaderBytes()},
			wantErr: "under recordings/",
		},
		{
			name:      "non console maya",
			runtime:   liveRuntime,
			processes: []windowsProcessSession{{ProcessID: 42, SessionID: 0, SessionName: "Services", Name: "maya.exe"}},
			visual: []visualEvidenceArtifact{
				{Kind: "recording", Path: "recordings/recording.mp4", MediaType: "video/mp4", DurationSeconds: defaultRecordingDuration.Seconds(), FPS: defaultRecordingFPS, TargetProfile: "ci", Host: "maya-win-01"},
			},
			files:   map[string][]byte{"recordings/recording.mp4": mp4HeaderBytes()},
			wantErr: "interactive Console session",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			evidenceDir := writeLiveRecordCommandProofBundle(t, tt.runtime, tt.visual, tt.files)
			bundle, err := validateLiveRecordCommandProofBundle(evidenceDir, tt.processes, "ci", "maya-win-01")
			if tt.wantValid {
				if err != nil {
					t.Fatalf("validateLiveRecordCommandProofBundle returned error: %v", err)
				}
				if len(bundle.VisualEvidence) != 1 {
					t.Fatalf("Visual Evidence count = %d, want 1", len(bundle.VisualEvidence))
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateLiveRecordCommandProofBundle error = %v, want containing %q", err, tt.wantErr)
			}
		})
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

func captureLiveRecordCommandProof(t *testing.T, repoDir string, options realSSHSmokeOptions) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(options.recordArgs(), &stdout, &stderr, repoDir, "test-version")
	if code != 0 {
		t.Fatalf("live record command exit code = %d, want 0; stdout: %s stderr: %s", code, stdout.String(), stderr.String())
	}
	evidenceDir := smokeOutputValue(stdout.String(), "evidence")
	if evidenceDir == "" {
		t.Fatalf("live record command did not print Evidence Bundle path:\n%s", stdout.String())
	}
	return evidenceDir
}

func addLiveDesktopScreenshotForProofArtifact(t *testing.T, repoDir string, evidenceDir string, options realSSHSmokeOptions) {
	t.Helper()
	bundle := readEvidenceBundle(t, evidenceDir)
	release, locked, err := acquireHostLock(repoDir, bundle.Host)
	if err != nil {
		t.Fatalf("acquire Host Lock for proof artifact screenshot on %s: %v", bundle.Host, err)
	}
	if locked {
		t.Fatalf("selected Maya Host %s became locked before proof artifact screenshot", bundle.Host)
	}
	defer func() {
		if err := release(); err != nil {
			t.Fatalf("release Host Lock for proof artifact screenshot on %s: %v", bundle.Host, err)
		}
	}()
	host := liveSmokeHostConfigByID(t, options, bundle.Host)
	processes, err := mayaTasklistSessions(host)
	if err != nil {
		t.Fatalf("query maya.exe tasklist sessions: %v", err)
	}
	if err := requireConsoleMayaProcess(processes); err != nil {
		t.Fatal(err)
	}
	remoteRoot := remoteJoin(host.WorkRoot, "artifacts", "live-visual-evidence-"+bundle.RunID)
	defer func() {
		_ = ggMayaSessiondBroker{host: host}.removeRemotePath(remoteRoot)
	}()
	screenshotBytes, err := captureWindowsDesktopScreenshot(sshWindowsDesktopTransport(host), remoteRoot)
	if err != nil {
		t.Fatalf("capture desktop screenshot for proof artifact: %v", err)
	}
	context := runContext{
		EvidenceDir: evidenceDir,
		EventsPath:  filepath.Join(evidenceDir, evidenceEventsFileName),
	}
	screenshot, err := registerVisualEvidenceBytes(context, "screenshot", "desktop-screenshot.png", "image/png", screenshotBytes)
	if err != nil {
		t.Fatalf("register desktop screenshot for proof artifact: %v", err)
	}
	screenshot.TargetProfile = bundle.TargetProfile
	screenshot.Host = bundle.Host
	bundle.VisualEvidence = append([]visualEvidenceArtifact{screenshot}, bundle.VisualEvidence...)
	bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
	if err := writeJSONFile(filepath.Join(evidenceDir, evidenceBundleFileName), bundle); err != nil {
		t.Fatalf("update live proof Evidence Bundle with desktop screenshot: %v", err)
	}
}

func liveSmokeHostConfigByID(t *testing.T, options realSSHSmokeOptions, hostID string) mayaHostConfig {
	t.Helper()
	if hostID == "" {
		t.Fatalf("live Evidence Bundle did not record a selected Maya Host")
	}
	config, err := loadUserHostConfig(options.HostConfig)
	if err != nil {
		t.Fatalf("load live host config: %v", err)
	}
	candidates, err := hostCandidates(config, options.TargetProfile, hostID)
	if err != nil {
		t.Fatalf("select live host config: %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != hostID {
		t.Fatalf("selected live host config = %+v, want host %q", candidates, hostID)
	}
	if !isHealthyHost(candidates[0]) {
		t.Fatalf("selected live host %q is not healthy in Target Profile %q", hostID, options.TargetProfile)
	}
	return candidates[0]
}

func publishOptionalLiveVisualEvidenceProofArtifact(t *testing.T, evidenceDir string) {
	t.Helper()
	options, err := liveVisualEvidenceProofArtifactOptionsFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatalf("parse live Visual Evidence proof artifact config: %v", err)
	}
	if !options.Enabled {
		t.Logf("Live Visual Evidence proof artifact upload disabled; set %s=true to publish a sanitized CI artifact", liveProofArtifactEnabledEnv)
		return
	}
	published, err := publishLiveVisualEvidenceProofArtifact(evidenceDir, options)
	if err != nil {
		t.Fatalf("publish live Visual Evidence proof artifact: %v", err)
	}
	t.Logf("Live Visual Evidence proof artifact: live-visual-evidence-proof path=%s retentionDays=%d", filepath.Base(published), options.RetentionDays)
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

func assertLiveRecordCommandProofBundle(t *testing.T, evidenceDir string, options realSSHSmokeOptions) evidenceBundle {
	t.Helper()
	selected := readEvidenceBundle(t, evidenceDir)
	host := liveSmokeHostConfigByID(t, options, selected.Host)
	processes, err := mayaTasklistSessions(host)
	if err != nil {
		t.Fatalf("query maya.exe tasklist sessions: %v", err)
	}
	bundle, err := validateLiveRecordCommandProofBundle(evidenceDir, processes, options.TargetProfile, options.Host)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func validateLiveRecordCommandProofBundle(evidenceDir string, processes []windowsProcessSession, targetProfile string, hostPin string) (evidenceBundle, error) {
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
	if bundle.Scenario != evidenceStandaloneScenarioPrefix+"recording" {
		return evidenceBundle{}, fmt.Errorf("Evidence Bundle scenario = %q, want standalone record command scenario", bundle.Scenario)
	}
	if bundle.TargetProfile != targetProfile {
		return evidenceBundle{}, fmt.Errorf("Evidence Bundle Target Profile = %q, want %q", bundle.TargetProfile, targetProfile)
	}
	if bundle.Host == "" {
		return evidenceBundle{}, fmt.Errorf("Evidence Bundle missing selected Maya Host metadata")
	}
	if hostPin != "" && bundle.Host != hostPin {
		return evidenceBundle{}, fmt.Errorf("Evidence Bundle selected Maya Host = %q, want pinned host %q", bundle.Host, hostPin)
	}
	recording, err := liveRecordCommandArtifact(bundle)
	if err != nil {
		return evidenceBundle{}, err
	}
	if recording.DurationSeconds <= 0 || recording.FPS <= 0 {
		return evidenceBundle{}, fmt.Errorf("recording %s missing duration/FPS metadata: %+v", recording.Path, recording)
	}
	if recording.TargetProfile != bundle.TargetProfile || recording.Host != bundle.Host {
		return evidenceBundle{}, fmt.Errorf("recording target metadata = %+v, want Target Profile %q and Maya Host %q", recording, bundle.TargetProfile, bundle.Host)
	}
	recordingBytes, err := os.ReadFile(filepath.Join(evidenceDir, filepath.FromSlash(recording.Path)))
	if err != nil {
		return evidenceBundle{}, err
	}
	if !looksLikeMP4Bytes(recordingBytes) {
		return evidenceBundle{}, fmt.Errorf("recording %s does not look like an MP4", recording.Path)
	}
	return bundle, nil
}

func liveRecordCommandArtifact(bundle evidenceBundle) (visualEvidenceArtifact, error) {
	var recordings []visualEvidenceArtifact
	for _, artifact := range bundle.VisualEvidence {
		if artifact.Kind == "recording" {
			cleanPath := cleanEvidenceArtifactPath(artifact.Path)
			if !strings.HasPrefix(cleanPath, evidenceRecordingsDir+"/") {
				return visualEvidenceArtifact{}, fmt.Errorf("recording artifact = %+v, want path under recordings/", artifact)
			}
			artifact.Path = cleanPath
			recordings = append(recordings, artifact)
		}
	}
	if len(recordings) != 1 {
		return visualEvidenceArtifact{}, fmt.Errorf("Evidence Bundle has %d recording artifacts, want 1", len(recordings))
	}
	recording := recordings[0]
	if recording.MediaType != "video/mp4" || !strings.HasPrefix(recording.Path, evidenceRecordingsDir+"/") {
		return visualEvidenceArtifact{}, fmt.Errorf("recording artifact = %+v, want video/mp4 under recordings/", recording)
	}
	return recording, nil
}

func requireLiveRecordCommandArtifact(t *testing.T, evidenceDir string, bundle evidenceBundle) visualEvidenceArtifact {
	t.Helper()
	recording, err := liveRecordCommandArtifact(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(evidenceDir, filepath.FromSlash(recording.Path))); err != nil {
		t.Fatalf("missing record command artifact %s: %v", recording.Path, err)
	}
	return recording
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

func writeLiveRecordCommandProofBundle(t *testing.T, runtime runtimeMetadata, visual []visualEvidenceArtifact, files map[string][]byte) string {
	t.Helper()
	dir := writeLiveVisualEvidenceProofBundle(t, runtime, visual, files)
	bundle := readEvidenceBundle(t, dir)
	bundle.Scenario = evidenceStandaloneScenarioPrefix + "recording"
	if len(visual) > 0 {
		bundle.TargetProfile = visual[0].TargetProfile
		bundle.Host = visual[0].Host
	}
	bundle.VisualEvidence = visual
	bundle.Artifacts = buildEvidenceBundleCatalog(bundle)
	if err := writeJSONFile(filepath.Join(dir, evidenceBundleFileName), bundle); err != nil {
		t.Fatalf("write record command proof bundle: %v", err)
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
