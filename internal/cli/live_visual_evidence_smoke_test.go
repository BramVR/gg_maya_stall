package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	transport := sshWindowsDesktopTransport{host: host.Config}
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

func legacyLiveCaptureWindowsDesktopScreenshot(host mayaHostConfig, remoteRoot string) ([]byte, error) {
	return runSSHCommandOutput(host, encodedPowerShellCommand(legacyLiveWindowsDesktopScreenshotPowerShell(remoteRoot)), sessiondCommandTimeout)
}

func legacyLiveCaptureWindowsDesktopRecording(host mayaHostConfig, remoteRoot string, duration time.Duration, fps int) ([]byte, error) {
	frames, intervalMS := legacyLiveWindowsDesktopFrameTiming(duration, fps)
	if err := runSSHCommand(host, encodedPowerShellCommand(fmt.Sprintf("New-Item -ItemType Directory -Force -Path %s | Out-Null", powerShellSingleQuoted(remoteRoot)))); err != nil {
		return nil, err
	}
	scriptPath := remoteJoin(remoteRoot, "desktop-recording.ps1")
	if err := legacyLiveWriteRemotePowerShellScript(host, scriptPath, legacyLiveWindowsDesktopRecordingPowerShell(remoteRoot, frames, intervalMS), sessiondCommandTimeout); err != nil {
		return nil, err
	}
	zipBytes, err := runSSHCommandOutput(host, encodedPowerShellCommand(fmt.Sprintf("Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass -Force; & %s", powerShellSingleQuoted(scriptPath))), duration+sessiondCommandTimeout)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "maya-stall-windows-video-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()
	framesDir := filepath.Join(tempDir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		return nil, err
	}
	if err := legacyLiveExtractFrameArchive(zipBytes, framesDir); err != nil {
		return nil, err
	}
	outputPath := filepath.Join(tempDir, "desktop-recording.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), duration+sessiondCommandTimeout)
	defer cancel()
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-framerate", strconv.Itoa(fps),
		"-start_number", "0",
		"-i", filepath.Join(framesDir, "frame-%06d.jpg"),
		"-pix_fmt", "yuv420p",
		"-an",
		"-movflags", "+faststart",
		outputPath,
	}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("encode Windows desktop frames with ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(outputPath)
}

func legacyLiveWriteRemotePowerShellScript(host mayaHostConfig, remotePath string, content string, timeout time.Duration) error {
	binary := host.SSH.Binary
	if binary == "" {
		binary = "ssh"
	}
	if timeout <= 0 {
		timeout = sshCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	script := fmt.Sprintf("$content = [Console]::In.ReadToEnd(); Set-Content -Encoding UTF8 -LiteralPath %s -Value $content", powerShellSingleQuoted(remotePath))
	command := exec.CommandContext(ctx, binary, append(sshArgs(host), encodedPowerShellCommand(script)...)...)
	command.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("write remote PowerShell script timed out after %s", timeout)
		}
		detail := firstUsefulStderrLine(stderr.String())
		if detail != "" {
			return fmt.Errorf("write remote PowerShell script failed: %w: %s", err, detail)
		}
		return fmt.Errorf("write remote PowerShell script failed: %w", err)
	}
	return nil
}

func legacyLiveWindowsDesktopFrameTiming(duration time.Duration, fps int) (int, int) {
	if fps < 1 {
		fps = 1
	}
	frames := int(duration.Seconds()*float64(fps) + 0.999)
	if frames < 1 {
		frames = 1
	}
	intervalMS := 1000 / fps
	if intervalMS < 1 {
		intervalMS = 1
	}
	return frames, intervalMS
}

func legacyLiveWindowsDesktopScreenshotPowerShell(remoteRoot string) string {
	return fmt.Sprintf(`$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$root = %s
New-Item -ItemType Directory -Force -Path $root | Out-Null
$taskName = "MayaStallVisualEvidenceScreenshot-" + [Guid]::NewGuid().ToString("N")
$out = Join-Path $root "desktop-screenshot.png"
$script = Join-Path $root ($taskName + ".ps1")
$template = @'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bitmap = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
$graphics = [System.Drawing.Graphics]::FromImage($bitmap)
$graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
$bitmap.Save("__MAYA_STALL_SCREENSHOT_OUT__", [System.Drawing.Imaging.ImageFormat]::Png)
$graphics.Dispose()
$bitmap.Dispose()
'@
$template.Replace("__MAYA_STALL_SCREENSHOT_OUT__", $out.Replace("\", "\\")) | Set-Content -Encoding ASCII -LiteralPath $script
cmd.exe /c "schtasks.exe /Delete /TN $taskName /F 2>NUL" | Out-Null
$startTime = (Get-Date).AddMinutes(1).ToString("HH:mm")
$taskRun = 'powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $script + '"'
$createArgs = @("/Create", "/TN", $taskName, "/SC", "ONCE", "/ST", $startTime, "/TR", $taskRun, "/RL", "HIGHEST", "/IT", "/F")
& schtasks.exe @createArgs | Out-Null
if ($LASTEXITCODE -ne 0) { throw "failed to create interactive desktop screenshot task" }
schtasks.exe /Run /TN $taskName | Out-Null
for ($i = 0; $i -lt 40; $i++) {
  if (Test-Path -LiteralPath $out) {
    try {
      $stream = [IO.File]::Open($out, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
      try {
        $buffer = New-Object byte[] 1048576
        while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
          [Console]::OpenStandardOutput().Write($buffer, 0, $read)
        }
      } finally {
        $stream.Dispose()
      }
      schtasks.exe /Delete /TN $taskName /F | Out-Null
      Remove-Item -Recurse -Force -LiteralPath $root -ErrorAction SilentlyContinue
      exit 0
    } catch {
      Start-Sleep -Milliseconds 500
    }
  }
  Start-Sleep -Milliseconds 500
}
schtasks.exe /Delete /TN $taskName /F | Out-Null
Remove-Item -Recurse -Force -LiteralPath $root -ErrorAction SilentlyContinue
throw "scheduled interactive desktop screenshot did not produce output"`, powerShellSingleQuoted(remoteRoot))
}

func legacyLiveWindowsDesktopRecordingPowerShell(remoteRoot string, frames int, intervalMS int) string {
	return fmt.Sprintf(`$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$root = %s
New-Item -ItemType Directory -Force -Path $root | Out-Null
$taskName = "MayaStallVisualEvidenceRecording-" + [Guid]::NewGuid().ToString("N")
$outDir = Join-Path $root "frames"
$zip = Join-Path $root "desktop-recording-frames.zip"
$done = $zip + ".done"
$script = Join-Path $root ($taskName + ".ps1")
$template = @'
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$OutDir = "__MAYA_STALL_FRAME_DIR__"
$Zip = "__MAYA_STALL_FRAME_ZIP__"
$Frames = __MAYA_STALL_FRAME_COUNT__
$IntervalMS = __MAYA_STALL_INTERVAL_MS__
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$start = [DateTime]::UtcNow
for ($i = 0; $i -lt $Frames; $i++) {
  $bitmap = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
  $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
  $graphics.CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
  $path = Join-Path $OutDir ("frame-{0:D6}.jpg" -f $i)
  $bitmap.Save($path, [System.Drawing.Imaging.ImageFormat]::Jpeg)
  $graphics.Dispose()
  $bitmap.Dispose()
  $target = $start.AddMilliseconds(($i + 1) * $IntervalMS)
  $remaining = [int](($target - [DateTime]::UtcNow).TotalMilliseconds)
  if ($remaining -gt 0) { Start-Sleep -Milliseconds $remaining }
}
Compress-Archive -Path (Join-Path $OutDir "frame-*.jpg") -DestinationPath $Zip -Force
Set-Content -LiteralPath ($Zip + ".done") -Value "ok"
'@
$scriptContent = $template.Replace("__MAYA_STALL_FRAME_DIR__", $outDir.Replace("\", "\\")).Replace("__MAYA_STALL_FRAME_ZIP__", $zip.Replace("\", "\\")).Replace("__MAYA_STALL_FRAME_COUNT__", "%d").Replace("__MAYA_STALL_INTERVAL_MS__", "%d")
$scriptContent | Set-Content -Encoding ASCII -LiteralPath $script
cmd.exe /c "schtasks.exe /Delete /TN $taskName /F 2>NUL" | Out-Null
$startTime = (Get-Date).AddMinutes(1).ToString("HH:mm")
$taskRun = 'powershell.exe -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $script + '"'
$createArgs = @("/Create", "/TN", $taskName, "/SC", "ONCE", "/ST", $startTime, "/TR", $taskRun, "/RL", "HIGHEST", "/IT", "/F")
& schtasks.exe @createArgs | Out-Null
if ($LASTEXITCODE -ne 0) { throw "failed to create interactive desktop recording task" }
schtasks.exe /Run /TN $taskName | Out-Null
$deadline = (Get-Date).AddSeconds(60)
while ((Get-Date) -lt $deadline) {
  if ((Test-Path -LiteralPath $done) -and (Test-Path -LiteralPath $zip)) {
    $stream = [IO.File]::Open($zip, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
      $buffer = New-Object byte[] 1048576
      while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
        [Console]::OpenStandardOutput().Write($buffer, 0, $read)
      }
    } finally {
      $stream.Dispose()
    }
    schtasks.exe /Delete /TN $taskName /F | Out-Null
    Remove-Item -Recurse -Force -LiteralPath $root -ErrorAction SilentlyContinue
    exit 0
  }
  Start-Sleep -Milliseconds 250
}
schtasks.exe /Delete /TN $taskName /F | Out-Null
Remove-Item -Recurse -Force -LiteralPath $root -ErrorAction SilentlyContinue
throw "scheduled interactive desktop recording did not produce output"`, powerShellSingleQuoted(remoteRoot), frames, intervalMS)
}

func legacyLiveExtractFrameArchive(zipBytes []byte, framesDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("read Windows desktop frame archive: %w", err)
	}
	count := 0
	for _, file := range reader.File {
		name := filepath.Base(file.Name)
		if !strings.HasPrefix(name, "frame-") || !strings.HasSuffix(strings.ToLower(name), ".jpg") {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("open frame %s: %w", name, err)
		}
		dstPath := filepath.Join(framesDir, name)
		dst, err := os.Create(dstPath)
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("create frame %s: %w", name, err)
		}
		_, copyErr := io.Copy(dst, src)
		closeErr := dst.Close()
		_ = src.Close()
		if copyErr != nil {
			return fmt.Errorf("write frame %s: %w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close frame %s: %w", name, closeErr)
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("Windows desktop frame archive contained no frames")
	}
	return nil
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
