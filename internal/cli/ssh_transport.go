package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

const sshCommandTimeout = 30 * time.Second
const defaultSFTPBatchTimeout = 30 * time.Minute

type realSSHHost struct {
	host mayaHostConfig
}

func runHostForConfig(host mayaHostConfig) runHost {
	if host.usesRealSSH() {
		return realSSHHost{host: host}
	}
	return fakeHost{}
}

func realSSHLayer(host mayaHostConfig) doctorCheck {
	if err := validateRealSSHConnection(host); err != nil {
		return failedCheck("ssh", err.Error(), "Fix SSH connection details in host config. See docs/setup/windows-maya-host.md#openssh-reachability.")
	}
	if _, err := sftpBatchTimeout(host); err != nil {
		return failedCheck("ssh", err.Error(), "Fix SSH transfer settings in host config. See docs/setup/windows-maya-host.md#openssh-reachability.")
	}
	if err := runSSHCommand(host, encodedPowerShellCommand(`Write-Output 'maya-stall-ssh-ok'`)); err != nil {
		return failedCheck("ssh", "unreachable", "Fix SSH reachability for this Maya Host. See docs/setup/windows-maya-host.md#openssh-reachability.")
	}
	return okCheck("ssh", "reachable")
}

func realWorkRootLayer(host mayaHostConfig) doctorCheck {
	if strings.TrimSpace(host.WorkRoot) == "" {
		return failedCheck("work-root", "missing workRoot", "Set a writable Maya Host work root in host config. See docs/setup/windows-maya-host.md#work-root.")
	}
	if err := rejectSFTPBatchUnsafePath(host.WorkRoot); err != nil {
		return failedCheck("work-root", "invalid workRoot", "Use a normal Windows path without control characters. See docs/setup/windows-maya-host.md#work-root.")
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$root = %s
New-Item -ItemType Directory -Force -Path $root | Out-Null
$probe = Join-Path $root ('.maya-stall-write-test-' + [guid]::NewGuid().ToString())
Set-Content -LiteralPath $probe -Value 'ok' -NoNewline
Remove-Item -LiteralPath $probe -Force
Write-Output 'writable'`, powerShellSingleQuoted(host.WorkRoot))
	if err := runSSHCommand(host, encodedPowerShellCommand(script)); err != nil {
		return failedCheck("work-root", "unwritable", "Fix the host work root path or permissions. See docs/setup/windows-maya-host.md#work-root.")
	}
	return okCheck("work-root", "writable")
}

func validateRealSSHConfig(host mayaHostConfig) error {
	if err := validateRealSSHConnection(host); err != nil {
		return err
	}
	if strings.TrimSpace(host.WorkRoot) == "" {
		return fmt.Errorf("workRoot is required for SSH transport")
	}
	if err := rejectSFTPBatchUnsafePath(host.WorkRoot); err != nil {
		return fmt.Errorf("workRoot %w", err)
	}
	if _, err := sftpBatchTimeout(host); err != nil {
		return err
	}
	return nil
}

func validateRealSSHConnection(host mayaHostConfig) error {
	if strings.TrimSpace(host.SSH.Host) == "" {
		return fmt.Errorf("ssh.host is required")
	}
	return nil
}

func runSSHCommand(host mayaHostConfig, remoteCommand []string) error {
	_, err := runSSHCommandOutput(host, remoteCommand)
	return err
}

func runSSHCommandOutput(host mayaHostConfig, remoteCommand []string) ([]byte, error) {
	binary := host.SSH.Binary
	if binary == "" {
		binary = "ssh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(sshArgs(host), remoteCommand...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		output := stderr.String()
		if stdout.Len() != 0 {
			output = stdout.String() + "\n" + output
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, withStderrTail(fmt.Errorf("ssh command timed out after %s", sshCommandTimeout), output)
		}
		return nil, withStderrTail(fmt.Errorf("ssh command failed: %w", err), output)
	}
	return stdout.Bytes(), nil
}

func sshArgs(host mayaHostConfig) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
	}
	if host.SSH.Port != 0 {
		args = append(args, "-p", strconv.Itoa(host.SSH.Port))
	}
	if host.SSH.IdentityFile != "" {
		args = append(args, "-i", expandHomePath(host.SSH.IdentityFile))
	}
	args = append(args, sshTarget(host))
	return args
}

func sshTarget(host mayaHostConfig) string {
	if host.SSH.User != "" {
		return host.SSH.User + "@" + host.SSH.Host
	}
	return host.SSH.Host
}

func encodedPowerShellCommand(script string) []string {
	encoded := utf16.Encode([]rune(script))
	content := make([]byte, 0, len(encoded)*2)
	for _, value := range encoded {
		content = append(content, byte(value), byte(value>>8))
	}
	return []string{"powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(content)}
}

func powerShellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func expandHomePath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (host realSSHHost) StagePayload(context runContext, payload []manifestPayload) error {
	if err := validateRealSSHConfig(host.host); err != nil {
		return err
	}
	runID := filepath.Base(context.StateDir)
	batch := newSFTPBatch()
	remoteRunRoot := remoteJoin(host.host.WorkRoot, "runs", runID)
	batch.mkdirAll(remoteRunRoot)
	batch.mkdirAll(remoteJoin(remoteRunRoot, "workspace"))
	for _, item := range payload {
		if err := rejectSFTPRepoPath(item.Source); err != nil {
			return fmt.Errorf("stage %s payload: %w", item.Kind, err)
		}
		if err := validatePayloadPathForTransport(context.RepoDir, item.Source); err != nil {
			return fmt.Errorf("stage %s payload %s: %w", item.Kind, item.Source, err)
		}
		source := filepath.Join(context.RepoDir, item.Source)
		destination := remoteJoin(remoteRunRoot, item.Staged)
		batch.mkdirAll(remoteDir(destination))
		batch.put(source, destination)
	}
	if err := runSFTPBatch(host.host, batch.String()); err != nil {
		return fmt.Errorf("upload Run Payload: %w", err)
	}
	return nil
}

func (host realSSHHost) CollectArtifacts(context runContext, scenario scenarioConfig) error {
	runID := filepath.Base(context.StateDir)
	remoteWorkspace := remoteJoin(host.host.WorkRoot, "runs", runID, "workspace")
	batch := newSFTPBatch()
	seen := make(map[string]bool)
	type downloadSpec struct {
		path     string
		optional bool
	}
	downloads := []downloadSpec{{path: scenario.ExpectedOutputs.ScenarioResult}}
	for _, path := range scenario.ExpectedOutputs.Files {
		downloads = append(downloads, downloadSpec{path: path, optional: true})
	}
	for _, validator := range scenario.Validators {
		if validator.Path != "" {
			downloads = append(downloads, downloadSpec{path: validator.Path, optional: true})
		}
	}
	for _, download := range downloads {
		item := download.path
		clean, err := cleanRepoRelativePath(item)
		if err != nil {
			return err
		}
		clean = filepath.ToSlash(clean)
		if clean == "" || seen[clean] {
			continue
		}
		if err := rejectSFTPRepoPath(clean); err != nil {
			return err
		}
		seen[clean] = true
		local := filepath.Join(context.Workspace, clean)
		if err := ensureWorkspacePathHasNoSymlinkAncestor(context.Workspace, clean); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return err
		}
		batch.get(remoteJoin(remoteWorkspace, clean), local, download.optional)
	}
	if batch.Empty() {
		return nil
	}
	if err := runSFTPBatch(host.host, batch.String()); err != nil {
		return fmt.Errorf("download declared outputs: %w", err)
	}
	return nil
}

func (host realSSHHost) hasSessionD() bool {
	return strings.TrimSpace(host.host.SessionD.StateDir) != ""
}

func (host realSSHHost) RunScenario(context runContext, scenario scenarioConfig) (ScenarioResult, error) {
	if !host.hasSessionD() {
		return ScenarioResult{}, fmt.Errorf("sessiond.stateDir is required for real SSH Session Broker")
	}
	mayaScripts := append([]string{}, scenario.Payload.MayaScripts...)
	mayaScripts = append(mayaScripts, scenario.Payload.Scripts...)
	if len(mayaScripts) == 0 {
		return ScenarioResult{}, fmt.Errorf("real SSH Session Broker requires at least one payload.mayaScripts entry")
	}
	if err := appendEvent(context.EventsPath, "broker.session.started", scenario.MayaVersion); err != nil {
		return ScenarioResult{}, err
	}
	var log strings.Builder
	for _, script := range mayaScripts {
		clean, err := cleanRepoRelativePath(script)
		if err != nil {
			return ScenarioResult{}, err
		}
		clean = filepath.ToSlash(clean)
		if err := rejectSFTPRepoPath(clean); err != nil {
			return ScenarioResult{}, err
		}
		remoteScript := remoteHostJoin(host.host.WorkRoot, "runs", filepath.Base(context.StateDir), "payload", "mayaScripts", clean)
		output, err := host.runSessionDScript(remoteScript, remoteHostJoin(host.host.WorkRoot, "runs", filepath.Base(context.StateDir), "workspace"), remoteHostJoin(host.host.WorkRoot, "runs", filepath.Base(context.StateDir), "workspace", filepath.ToSlash(scenario.ExpectedOutputs.ScenarioResult)))
		if err != nil {
			return ScenarioResult{}, err
		}
		if output != "" {
			log.WriteString(output)
			if !strings.HasSuffix(output, "\n") {
				log.WriteString("\n")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(context.LogPath), 0o755); err != nil {
		return ScenarioResult{}, err
	}
	if log.Len() == 0 {
		log.WriteString("gg_mayasessiond executed Scenario scripts\n")
	}
	if err := os.WriteFile(context.LogPath, []byte(log.String()), 0o644); err != nil {
		return ScenarioResult{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.session.finished", resultStatusPassed); err != nil {
		return ScenarioResult{}, err
	}
	return ScenarioResult{Status: resultStatusPassed, Summary: "gg_mayasessiond Scenario completed"}, nil
}

func (host realSSHHost) runSessionDScript(scriptPath string, workspace string, scenarioResultPath string) (string, error) {
	timeout, err := sessionDTimeout(host.host.SessionD)
	if err != nil {
		return "", err
	}
	argsJSON, err := json.Marshal(map[string]any{
		"workspace":       workspace,
		"scenario_result": scenarioResultPath,
		"environment":     map[string]string{scenarioResultEnvVar: scenarioResultPath},
	})
	if err != nil {
		return "", err
	}
	scriptPathJSON, err := json.Marshal(scriptPath)
	if err != nil {
		return "", err
	}
	wrapperPath := remoteHostJoin(workspace, ".maya-stall-sessiond-wrapper.py")
	wrapper := fmt.Sprintf(`import json
import os
import runpy

__args__ = json.loads(%q)
os.environ.update(__args__.get("environment", {}))
runpy.run_path(%s, init_globals={"__args__": __args__}, run_name="__maya_stall__")
`, string(argsJSON), string(scriptPathJSON))
	if err := host.writeRemoteTextFile(wrapperPath, wrapper); err != nil {
		return "", err
	}
	response, err := host.callSessionDTool("script.execute", []string{
		"file_path=" + wrapperPath,
		"timeout=" + strconv.Itoa(int(timeout.Seconds())),
	})
	if err != nil {
		return "", err
	}
	if !response.Structured.Success {
		return response.Structured.Output, fmt.Errorf("sessiond script %s failed: %v", scriptPath, response.Structured.Errors)
	}
	return response.Structured.Output, nil
}

func (host realSSHHost) CaptureScreenshot(context runContext, request screenshotRequest) (visualEvidenceArtifact, error) {
	if !host.hasSessionD() {
		return visualEvidenceArtifact{}, fmt.Errorf("sessiond.stateDir is required for real SSH screenshot capture")
	}
	name := request.Name
	if name == "" {
		name = "screenshot.png"
	}
	name = filepath.Base(filepath.ToSlash(name))
	if name == "." || name == ".." || name == "" {
		name = "screenshot.png"
	}
	if filepath.Ext(name) == "" {
		name += ".png"
	}
	response, err := host.callSessionDTool("viewport.capture", []string{"format=png", "width=1024", "height=576"})
	if err != nil {
		return visualEvidenceArtifact{}, err
	}
	imageData := ""
	for _, item := range response.Content {
		if item.Type == "image" && item.Data != "" {
			imageData = item.Data
			break
		}
	}
	if imageData == "" {
		return visualEvidenceArtifact{}, fmt.Errorf("sessiond viewport.capture did not return image content")
	}
	content, err := base64.StdEncoding.DecodeString(imageData)
	if err != nil {
		return visualEvidenceArtifact{}, fmt.Errorf("decode viewport capture: %w", err)
	}
	relative := filepath.Join("screenshots", name)
	path := filepath.Join(context.EvidenceDir, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return visualEvidenceArtifact{}, err
	}
	if err := appendEvent(context.EventsPath, "broker.screenshot.captured", filepath.ToSlash(relative)); err != nil {
		return visualEvidenceArtifact{}, err
	}
	mediaType := response.Structured.MimeType
	if mediaType == "" {
		mediaType = "image/png"
	}
	return visualEvidenceArtifact{Kind: "screenshot", Path: filepath.ToSlash(relative), MediaType: mediaType}, nil
}

func (host realSSHHost) writeRemoteTextFile(path string, content string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$path = %s
$content = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(%s))
New-Item -ItemType Directory -Force -Path (Split-Path -LiteralPath $path) | Out-Null
$encoding = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText($path, $content, $encoding)`, powerShellSingleQuoted(path), powerShellSingleQuoted(encoded))
	return runSSHCommand(host.host, encodedPowerShellCommand(script))
}

func (host realSSHHost) callSessionDTool(tool string, pairs []string) (sessionDCallResponse, error) {
	projectDir := strings.TrimSpace(host.host.SessionD.ProjectDir)
	if projectDir == "" {
		projectDir = "C:/PROJECTS/GG/GG_MayaSessiond"
	}
	python := strings.TrimSpace(host.host.SessionD.Python)
	if python == "" {
		python = "python"
	}
	stateDir := strings.TrimSpace(host.host.SessionD.StateDir)
	quotedPairs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		quotedPairs = append(quotedPairs, powerShellSingleQuoted(pair))
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath %s
& %s -m gg_maya_sessiond.cli call --state-dir %s %s %s --json`, powerShellSingleQuoted(projectDir), powerShellSingleQuoted(python), powerShellSingleQuoted(stateDir), powerShellSingleQuoted(tool), strings.Join(quotedPairs, " "))
	stdout, err := runSSHCommandOutput(host.host, encodedPowerShellCommand(script))
	if err != nil {
		return sessionDCallResponse{}, err
	}
	var response sessionDCallResponse
	if err := decodeJSONUseNumber(stdout, &response); err != nil {
		return sessionDCallResponse{}, fmt.Errorf("parse sessiond response: %w", err)
	}
	if !response.OK {
		return sessionDCallResponse{}, fmt.Errorf("sessiond %s failed: %s", tool, response.Error)
	}
	return response, nil
}

type sessionDCallResponse struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	Structured struct {
		Success  bool           `json:"success"`
		Output   string         `json:"output"`
		Errors   map[string]any `json:"errors"`
		MimeType string         `json:"mime_type"`
	} `json:"structured"`
	Content []struct {
		Type     string `json:"type"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	} `json:"content"`
}

func sessionDTimeout(config sessionDConfig) (time.Duration, error) {
	value := strings.TrimSpace(config.Timeout)
	if value == "" {
		return 5 * time.Minute, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("sessiond.timeout %q must be a Go duration such as 5m", value)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("sessiond.timeout must be positive")
	}
	return timeout, nil
}

func validatePayloadPathForTransport(repoDir string, relativePath string) error {
	if err := ensurePayloadPathHasNoSymlinkAncestor(repoDir, relativePath); err != nil {
		return err
	}
	source := filepath.Join(repoDir, relativePath)
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("payload path %s must not be or contain a symlink", path)
		}
		if entry.IsDir() || info.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("payload path %s must be a regular file or directory", path)
	})
}

func rejectSFTPBatchUnsafePath(path string) error {
	for _, r := range path {
		if r == '\n' || r == '\r' || r == 0 {
			return fmt.Errorf("path contains unsupported SFTP batch control characters")
		}
	}
	return nil
}

func rejectSFTPRepoPath(path string) error {
	if err := rejectSFTPBatchUnsafePath(path); err != nil {
		return err
	}
	if strings.Contains(path, `\`) {
		return fmt.Errorf("repo paths used over SSH must use forward slashes, not backslashes")
	}
	return nil
}

func runSFTPBatch(host mayaHostConfig, batch string) error {
	binary := host.SSH.SFTPBinary
	if binary == "" {
		binary = "sftp"
	}
	args := []string{"-b", "-"}
	if host.SSH.Port != 0 {
		args = append(args, "-P", strconv.Itoa(host.SSH.Port))
	}
	if host.SSH.IdentityFile != "" {
		args = append(args, "-i", expandHomePath(host.SSH.IdentityFile))
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=2",
	)
	args = append(args, sshTarget(host))
	timeout, err := sftpBatchTimeout(host)
	if err != nil {
		return err
	}
	var ctx context.Context
	var cancel context.CancelFunc
	var command *exec.Cmd
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
		command = exec.CommandContext(ctx, binary, args...)
	} else {
		command = exec.Command(binary, args...)
	}
	command.Stdin = strings.NewReader(batch)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
			return withStderrTail(fmt.Errorf("sftp command timed out after %s", timeout), stderr.String())
		}
		return withStderrTail(fmt.Errorf("sftp command failed: %w", err), stderr.String())
	}
	return nil
}

func sftpBatchTimeout(host mayaHostConfig) (time.Duration, error) {
	value := strings.TrimSpace(host.SSH.SFTPTimeout)
	if value == "" {
		return defaultSFTPBatchTimeout, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("ssh.sftpTimeout %q must be a Go duration such as 30m or 0 to disable", value)
	}
	if timeout < 0 {
		return 0, fmt.Errorf("ssh.sftpTimeout must not be negative")
	}
	return timeout, nil
}

func withStderrTail(err error, stderr string) error {
	stderr = sanitizeStderrTail(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func sanitizeStderrTail(stderr string) string {
	stderr = strings.TrimSpace(strings.ToValidUTF8(stderr, ""))
	const maxStderrTailRunes = 4096
	runes := []rune(stderr)
	if len(runes) > maxStderrTailRunes {
		runes = runes[len(runes)-maxStderrTailRunes:]
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, string(runes))
}

type sftpBatch struct {
	builder strings.Builder
}

func newSFTPBatch() *sftpBatch {
	return &sftpBatch{}
}

func (batch *sftpBatch) Empty() bool {
	return batch.builder.Len() == 0
}

func (batch *sftpBatch) String() string {
	return batch.builder.String()
}

func (batch *sftpBatch) mkdir(path string) {
	if path == "" {
		return
	}
	fmt.Fprintf(&batch.builder, "-mkdir %s\n", sftpQuote(path))
}

func (batch *sftpBatch) mkdirAll(path string) {
	current := ""
	normalized := sftpRemotePath(path)
	if strings.HasPrefix(normalized, "/") {
		current = "/"
	}
	for _, part := range strings.Split(strings.Trim(normalized, "/"), "/") {
		if part == "" {
			continue
		}
		if current == "" {
			current = part
			if strings.HasSuffix(current, ":") {
				continue
			}
		} else if current == "/" {
			current += part
		} else {
			current += "/" + part
		}
		batch.mkdir(current)
	}
}

func (batch *sftpBatch) put(local string, remote string) {
	fmt.Fprintf(&batch.builder, "put -r %s %s\n", sftpQuote(local), sftpQuote(remote))
}

func (batch *sftpBatch) get(remote string, local string, optional bool) {
	prefix := "get"
	if optional {
		prefix = "-get"
	}
	fmt.Fprintf(&batch.builder, "%s -r %s %s\n", prefix, sftpQuote(remote), sftpQuote(local))
}

func sftpQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func remoteJoin(root string, parts ...string) string {
	clean := sftpRemotePath(root)
	clean = strings.TrimRight(clean, "/")
	for _, part := range parts {
		part = strings.Trim(strings.ReplaceAll(filepath.ToSlash(part), `\`, "/"), "/")
		if part == "" {
			continue
		}
		clean += "/" + part
	}
	return clean
}

func remoteHostJoin(root string, parts ...string) string {
	clean := strings.ReplaceAll(root, `\`, "/")
	clean = strings.TrimRight(clean, "/")
	for _, part := range parts {
		part = strings.Trim(strings.ReplaceAll(filepath.ToSlash(part), `\`, "/"), "/")
		if part == "" {
			continue
		}
		clean += "/" + part
	}
	return clean
}

func sftpRemotePath(path string) string {
	normalized := strings.ReplaceAll(path, `\`, "/")
	if len(normalized) >= 2 && normalized[1] == ':' && isASCIILetter(normalized[0]) {
		return "/" + normalized
	}
	return normalized
}

func isASCIILetter(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func remoteDir(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return ""
	}
	return path[:index]
}
