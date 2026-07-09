package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

func probeInstalledMayaVersions(host mayaHostConfig) ([]string, error) {
	raw, err := runSSHCommandOutput(host, encodedPowerShellCommand(installedMayaVersionsProbeScript()), sshCommandTimeout)
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, line := range strings.Split(string(raw), "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "maya-version:")
		if ok {
			versions = append(versions, value)
		}
	}
	return normalizeMayaVersions(versions), nil
}

func installedMayaVersionsProbeScript() string {
	return `$versions = New-Object 'System.Collections.Generic.HashSet[string]'
function Add-MayaVersion([string]$version) {
  if ($version -match '^\d{4}(?:\.\d+)?$') {
    [void]$versions.Add($version)
  }
}
$autodeskRoots = @()
if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
  $autodeskRoots += (Join-Path $env:ProgramFiles 'Autodesk')
}
if (-not [string]::IsNullOrWhiteSpace(${env:ProgramFiles(x86)})) {
  $autodeskRoots += (Join-Path ${env:ProgramFiles(x86)} 'Autodesk')
}
$autodeskRoots | Select-Object -Unique | ForEach-Object {
  Get-ChildItem -LiteralPath $_ -Directory -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.Name -match '^Maya(\d{4}(?:\.\d+)?)$') {
      $mayaExe = Join-Path $_.FullName 'bin\maya.exe'
      if (Test-Path -LiteralPath $mayaExe -PathType Leaf) {
        Add-MayaVersion $Matches[1]
      }
    }
  }
}
$registryRoots = @('HKLM:\SOFTWARE\Autodesk\Maya', 'HKLM:\SOFTWARE\WOW6432Node\Autodesk\Maya')
$registryRoots | ForEach-Object {
  Get-ChildItem -LiteralPath $_ -ErrorAction SilentlyContinue | ForEach-Object {
    if ($_.PSChildName -match '^\d{4}(?:\.\d+)?$') {
      $version = $_.PSChildName
      $installPathKey = Join-Path $_.PSPath 'Setup\InstallPath'
      $props = Get-ItemProperty -LiteralPath $installPathKey -ErrorAction SilentlyContinue
      if ($null -ne $props) {
        $props.PSObject.Properties | ForEach-Object {
          if ($_.Name -notlike 'PS*' -and -not [string]::IsNullOrWhiteSpace([string]$_.Value)) {
            $mayaExe = Join-Path ([string]$_.Value) 'bin\maya.exe'
            if (Test-Path -LiteralPath $mayaExe -PathType Leaf) {
              Add-MayaVersion $version
            }
          }
        }
      }
    }
  }
}
$versions | Sort-Object | ForEach-Object { Write-Output ('maya-version:' + $_) }`
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
	if err := validateTrustedPluginArtifactsRoot(host); err != nil {
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
	binary := host.SSH.Binary
	if binary == "" {
		binary = "ssh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append(sshArgs(host), remoteCommand...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ssh command timed out after %s", sshCommandTimeout)
		}
		detail := firstUsefulStderrLine(stderr.String())
		if detail != "" {
			return fmt.Errorf("ssh command failed: %w: %s", err, detail)
		}
		return fmt.Errorf("ssh command failed: %w", err)
	}
	return nil
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
	if err := validatePayloadForStage(context, payload); err != nil {
		return err
	}
	if err := host.prepareTrustedPluginArtifactDestinations(payload); err != nil {
		return err
	}
	batch := newSFTPBatch()
	batch.mkdirAll(context.RunWorkspace.RemoteRunRoot())
	batch.mkdirAll(context.RunWorkspace.RemoteWorkspace())
	for _, item := range payload {
		source := filepath.Join(context.RepoDir, item.Source)
		destination := context.RunWorkspace.RemotePayloadPath(item)
		batch.mkdirAll(remoteDir(destination))
		batch.put(source, destination)
		if trustedDestination := trustedPluginArtifactPath(host.host, item); trustedDestination != "" {
			batch.mkdirAll(remoteDir(trustedDestination))
			batch.put(source, trustedDestination)
		}
	}
	if err := runSFTPBatch(host.host, batch.String()); err != nil {
		return fmt.Errorf("upload Run Payload: %w", err)
	}
	return nil
}

func validatePayloadForStage(context runContext, payload []manifestPayload) error {
	for _, item := range payload {
		if err := rejectSFTPRepoPath(item.Source); err != nil {
			return fmt.Errorf("stage %s payload: %w", item.Kind, err)
		}
		if err := validatePayloadPathForTransport(context.RepoDir, item.Source); err != nil {
			return fmt.Errorf("stage %s payload %s: %w", item.Kind, item.Source, err)
		}
	}
	return nil
}

func (host realSSHHost) prepareTrustedPluginArtifactDestinations(payload []manifestPayload) error {
	var paths []string
	for _, item := range payload {
		destination := trustedPluginArtifactPath(host.host, item)
		if destination == "" {
			continue
		}
		if err := rejectSFTPBatchUnsafePath(destination); err != nil {
			return err
		}
		paths = append(paths, destination)
	}
	if len(paths) == 0 {
		return nil
	}
	var builder strings.Builder
	builder.WriteString("$ErrorActionPreference = 'Stop'\n")
	builder.WriteString("foreach ($path in @(")
	for index, path := range paths {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(powerShellSingleQuoted(path))
	}
	builder.WriteString(")) {\n")
	builder.WriteString("  if (Test-Path -LiteralPath $path) {\n")
	builder.WriteString("    Remove-Item -LiteralPath $path -Recurse -Force -ErrorAction Stop\n")
	builder.WriteString("  }\n")
	builder.WriteString("}\n")
	builder.WriteString("exit 0\n")
	if err := runSSHCommand(host.host, encodedPowerShellCommand(builder.String())); err != nil {
		return fmt.Errorf("prepare trusted Plugin Artifact destination: %w", err)
	}
	return nil
}

func trustedPluginArtifactPath(host mayaHostConfig, item manifestPayload) string {
	if item.Kind != "pluginArtifacts" {
		return ""
	}
	root := trustedPluginArtifactsRoot(host)
	if root == "" {
		return ""
	}
	return remoteJoin(root, item.Source)
}

func trustedPluginArtifactsRoot(host mayaHostConfig) string {
	return strings.TrimSpace(remotePath(host.TrustedPluginArtifactsRoot))
}

func validateTrustedPluginArtifactsRoot(host mayaHostConfig) error {
	root := trustedPluginArtifactsRoot(host)
	if root == "" {
		return nil
	}
	if err := rejectSFTPBatchUnsafePath(root); err != nil {
		return fmt.Errorf("trustedPluginArtifactsRoot %w", err)
	}
	workRoot := cleanRemotePathForComparison(host.WorkRoot)
	trustedRoot := cleanRemotePathForComparison(root)
	if workRoot == "" {
		return nil
	}
	runsRoot := cleanRemotePathForComparison(remoteJoin(workRoot, "runs"))
	if trustedRoot == workRoot || trustedRoot == runsRoot || strings.HasPrefix(trustedRoot, runsRoot+"/") {
		return fmt.Errorf("trustedPluginArtifactsRoot must be outside workRoot/runs and separate from workRoot")
	}
	if strings.HasPrefix(runsRoot, trustedRoot+"/") {
		return fmt.Errorf("trustedPluginArtifactsRoot must not contain workRoot/runs")
	}
	return nil
}

func cleanRemotePathForComparison(value string) string {
	clean := path.Clean(strings.ReplaceAll(remotePath(value), `\`, "/"))
	if clean == "." {
		return ""
	}
	return strings.TrimRight(strings.ToLower(clean), "/")
}

func (host realSSHHost) CollectArtifacts(context runContext, scenario scenarioContract) error {
	return host.collectArtifacts(context, scenario, false)
}

func (host realSSHHost) CollectFailureArtifacts(context runContext, scenario scenarioContract) error {
	return host.collectArtifacts(context, scenario, true)
}

func (host realSSHHost) collectArtifacts(context runContext, scenario scenarioContract, optional bool) error {
	batch := newSFTPBatch()
	seen := make(map[string]bool)
	for _, download := range scenario.Outputs {
		clean := download.Path
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
		batch.get(context.RunWorkspace.RemoteOutputPath(clean), local, optional || download.Optional)
	}
	if batch.Empty() {
		return nil
	}
	if err := runSFTPBatch(host.host, batch.String()); err != nil {
		return fmt.Errorf("download declared outputs: %w", err)
	}
	return nil
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
		detail := firstUsefulStderrLine(stderr.String())
		if timeout > 0 && ctx.Err() == context.DeadlineExceeded {
			if detail != "" {
				return fmt.Errorf("sftp command timed out after %s: %s", timeout, detail)
			}
			return fmt.Errorf("sftp command timed out after %s", timeout)
		}
		if detail != "" {
			return fmt.Errorf("sftp command failed: %w: %s", err, detail)
		}
		return fmt.Errorf("sftp command failed: %w", err)
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
	fmt.Fprintf(&batch.builder, "-mkdir %s\n", sftpQuote(sftpRemotePath(path)))
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
	fmt.Fprintf(&batch.builder, "put -r %s %s\n", sftpQuote(local), sftpQuote(sftpRemotePath(remote)))
}

func (batch *sftpBatch) get(remote string, local string, optional bool) {
	prefix := "get"
	if optional {
		prefix = "-get"
	}
	fmt.Fprintf(&batch.builder, "%s -r %s %s\n", prefix, sftpQuote(sftpRemotePath(remote)), sftpQuote(local))
}

func sftpQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func sftpRemotePath(path string) string {
	clean := strings.ReplaceAll(path, `\`, "/")
	if len(clean) >= 2 && clean[1] == ':' && isASCIIAlpha(clean[0]) {
		return "/" + clean
	}
	return clean
}

func isASCIIAlpha(value byte) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z')
}

func remoteJoin(root string, parts ...string) string {
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

func remoteDir(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return ""
	}
	return path[:index]
}
