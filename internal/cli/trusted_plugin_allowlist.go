package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const trustedPluginAllowlistLayerID = "trusted-plugin-allowlist"
const safeModeAllowedlistOptionVar = "SafeModeAllowedlistPaths"
const trustedPluginPrefsJSONPrefix = "MAYA_STALL_TRUST_PREFS_JSON:"

var safeModeAllowedlistOptionPattern = regexp.MustCompile(`(?m)-(sva|sa)\s+"SafeModeAllowedlistPaths"(?:\s+"((?:\\.|[^"])*)")?`)

type trustedPluginPrefsProbe struct {
	Exists  bool   `json:"exists"`
	Content string `json:"content"`
	Changed bool   `json:"changed,omitempty"`
}

type trustedPluginPrefsProbeJSON struct {
	Exists  bool            `json:"exists"`
	Content json.RawMessage `json:"content"`
	Changed bool            `json:"changed,omitempty"`
}

func trustedPluginAllowlistLocalConfigCheck(repoDir string, host mayaHostConfig, scenario scenarioContract) ([]string, *doctorCheck) {
	if trustedPluginArtifactsRoot(host) == "" {
		return nil, nil
	}
	if err := validateTrustedPluginArtifactsRoot(host); err != nil {
		check := withSource(failedCheck(trustedPluginAllowlistLayerID, err.Error(), "Choose a narrow trustedPluginArtifactsRoot outside workRoot/runs. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."), "config")
		return nil, &check
	}
	requiredPaths, err := trustedPluginAllowlistRequiredPaths(repoDir, host, scenario.Payload)
	if err != nil {
		check := withSource(failedCheck(trustedPluginAllowlistLayerID, err.Error(), "Fix declared Plugin Artifact paths before checking Maya trusted plug-in locations. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."), "config")
		return nil, &check
	}
	return requiredPaths, nil
}

func trustedPluginAllowlistDoctorLayer(repoDir string, host mayaHostConfig, scenario scenarioContract, prevalidatedRequiredPaths []string, repair bool, sshOK bool, lockClear bool) doctorCheck {
	root := trustedPluginArtifactsRoot(host)
	if root == "" {
		return withSource(okCheck(trustedPluginAllowlistLayerID, "not configured"), "config")
	}
	requiredPaths := prevalidatedRequiredPaths
	if requiredPaths == nil {
		var check *doctorCheck
		requiredPaths, check = trustedPluginAllowlistLocalConfigCheck(repoDir, host, scenario)
		if check != nil {
			return *check
		}
	}
	if !host.usesRealSSH() {
		return withSource(okCheck(trustedPluginAllowlistLayerID, "not checked for fake runtime"), "fake")
	}
	if !sshOK {
		return withBlockedBy(failedCheck(trustedPluginAllowlistLayerID, "skipped because SSH or work-root is not healthy", "Repair SSH and work-root before checking Maya trusted plug-in locations. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."), "ssh")
	}
	if repair && !lockClear {
		return withBlockedBy(failedCheck(trustedPluginAllowlistLayerID, "repair skipped because Host Lock is not clear", "Wait for the active Fresh Run or clear the stale Host Lock before repairing Maya trusted plug-in locations. See docs/setup/windows-maya-host.md#host-lock-and-retention."), "host-lock")
	}
	versions := trustedPluginAllowlistMayaVersions(host, scenario.Config)
	changed, err := ensureTrustedPluginAllowlist(host, versions, requiredPaths, repair)
	if err != nil {
		hint := "Add trustedPluginArtifactsRoot to Maya's trusted plug-in locations, or run doctor with --repair-trusted-plugin-allowlist after approving this host policy change. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."
		if len(requiredPaths) > 1 {
			hint = "Run doctor again with the same Scenario and --repair-trusted-plugin-allowlist after approving this host policy change. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."
		}
		return withSource(failedCheck(trustedPluginAllowlistLayerID, err.Error(), hint), "maya-prefs")
	}
	detail := fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
	if len(requiredPaths) > 1 {
		detail = fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot and trusted Plugin Artifact destination directories", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
	}
	if repair && changed {
		detail = fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot after repair", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
		if len(requiredPaths) > 1 {
			detail = fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot and trusted Plugin Artifact destination directories after repair", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
		}
	} else if repair {
		detail = fmt.Sprintf("Maya %s %s already contains trustedPluginArtifactsRoot", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
		if len(requiredPaths) > 1 {
			detail = fmt.Sprintf("Maya %s %s already contains trustedPluginArtifactsRoot and trusted Plugin Artifact destination directories", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
		}
	}
	return withSource(okCheck(trustedPluginAllowlistLayerID, detail), "maya-prefs")
}

func ensureTrustedPluginArtifactsAllowlistedForRun(repoDir string, host mayaHostConfig, scenario scenarioContract) error {
	if trustedPluginArtifactsRoot(host) == "" || !host.usesRealSSH() || !manifestHasPluginArtifacts(scenario.Payload) {
		return nil
	}
	requiredPaths, err := trustedPluginAllowlistRequiredPaths(repoDir, host, scenario.Payload)
	if err != nil {
		return fmt.Errorf("trusted Plugin Artifact allowlist preflight failed: %w", err)
	}
	if _, err := ensureTrustedPluginAllowlist(host, trustedPluginAllowlistMayaVersions(host, scenario.Config), requiredPaths, false); err != nil {
		return fmt.Errorf("trusted Plugin Artifact allowlist preflight failed: %w", err)
	}
	return nil
}

func ensureTrustedPluginAllowlist(host mayaHostConfig, versions []string, requiredPaths []string, repair bool) (bool, error) {
	if repair {
		sessions, err := mayaProcessSessions(host)
		if err != nil {
			return false, fmt.Errorf("check Maya process before TrustCenter repair: %w", err)
		}
		if len(sessions) > 0 {
			return false, fmt.Errorf("TrustCenter repair requires Maya to be stopped first so userPrefs.mel is not overwritten on exit")
		}
	}
	requiredPaths = compactTrustedPluginAllowlistPaths(requiredPaths)
	if len(requiredPaths) == 0 {
		requiredPaths = []string{trustedPluginArtifactsRoot(host)}
	}
	var changed bool
	checked := 0
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if version == "" {
			continue
		}
		if err := validateMayaPrefsVersion(version); err != nil {
			return false, err
		}
		checked++
		probe, err := trustedPluginPrefs(host, version, requiredPaths, repair)
		if err != nil {
			return false, fmt.Errorf("maya %s %s", version, err)
		}
		if !probe.Exists {
			return false, fmt.Errorf("maya %s userPrefs.mel is missing", version)
		}
		missingPaths := prefsAllowlistMissingPaths(probe.Content, requiredPaths)
		if len(missingPaths) > 0 {
			if len(requiredPaths) == 1 || missingPathsIncludeTrustedRoot(host, missingPaths) {
				return false, fmt.Errorf("maya %s %s does not contain trustedPluginArtifactsRoot", version, safeModeAllowedlistOptionVar)
			}
			return false, fmt.Errorf("maya %s %s does not contain trusted Plugin Artifact destination directories; run doctor with the same Scenario and --repair-trusted-plugin-allowlist", version, safeModeAllowedlistOptionVar)
		}
		changed = changed || probe.Changed
	}
	if checked == 0 {
		return false, fmt.Errorf("maya version is required to locate TrustCenter preferences; set host mayaVersions or Scenario mayaVersion")
	}
	return changed, nil
}

func missingPathsIncludeTrustedRoot(host mayaHostConfig, missingPaths []string) bool {
	root := normalizeTrustedPluginPath(trustedPluginArtifactsRoot(host))
	for _, missingPath := range missingPaths {
		if normalizeTrustedPluginPath(missingPath) == root {
			return true
		}
	}
	return false
}

func validateMayaPrefsVersion(version string) error {
	if !mayaVersionPattern.MatchString(version) {
		return fmt.Errorf("maya version %q is not a safe preferences path segment", version)
	}
	return nil
}

func trustedPluginPrefs(host mayaHostConfig, version string, requiredPaths []string, repair bool) (trustedPluginPrefsProbe, error) {
	script := trustedPluginPrefsReadScript(host, version)
	input := ""
	remoteCommand := encodedPowerShellCommand(script)
	if repair {
		script = trustedPluginPrefsRepairScript(host, version)
		repairInput, err := trustedPluginPrefsRepairInput(requiredPaths)
		if err != nil {
			return trustedPluginPrefsProbe{}, err
		}
		input, err = trustedPluginPrefsRepairEnvelope(script, repairInput)
		if err != nil {
			return trustedPluginPrefsProbe{}, err
		}
		remoteCommand = encodedPowerShellCommand(trustedPluginPrefsRepairBootstrapScript())
	}
	raw, err := runSSHCommandOutputWithInput(host, remoteCommand, input, sshCommandTimeout)
	if err != nil {
		return trustedPluginPrefsProbe{}, err
	}
	probe, err := parseTrustedPluginPrefsProbe(string(raw))
	if err != nil {
		return trustedPluginPrefsProbe{}, err
	}
	return probe, nil
}

func parseTrustedPluginPrefsProbe(output string) (trustedPluginPrefsProbe, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		jsonText, ok := strings.CutPrefix(line, trustedPluginPrefsJSONPrefix)
		if !ok {
			continue
		}
		var rawProbe trustedPluginPrefsProbeJSON
		if err := json.Unmarshal([]byte(jsonText), &rawProbe); err != nil {
			return trustedPluginPrefsProbe{}, fmt.Errorf("parse Maya TrustCenter prefs probe: %w", err)
		}
		content, err := trustedPluginPrefsContent(rawProbe.Content)
		if err != nil {
			return trustedPluginPrefsProbe{}, err
		}
		return trustedPluginPrefsProbe{Exists: rawProbe.Exists, Content: content, Changed: rawProbe.Changed}, nil
	}
	return trustedPluginPrefsProbe{}, fmt.Errorf("maya TrustCenter prefs probe returned no JSON")
}

func trustedPluginPrefsContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var content string
	if err := json.Unmarshal(raw, &content); err == nil {
		return content, nil
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("parse Maya TrustCenter prefs content")
}

func prefsAllowlistContainsRoot(content string, root string) bool {
	return len(prefsAllowlistMissingPaths(content, []string{root})) == 0
}

func prefsAllowlistMissingPaths(content string, requiredPaths []string) []string {
	allowed := map[string]bool{}
	for _, entry := range parseSafeModeAllowedlistPaths(content) {
		normalized := normalizeTrustedPluginPath(entry)
		if normalized != "" {
			allowed[normalized] = true
		}
	}
	var missing []string
	for _, required := range compactTrustedPluginAllowlistPaths(requiredPaths) {
		normalized := normalizeTrustedPluginPath(required)
		if normalized == "" || allowed[normalized] {
			continue
		}
		missing = append(missing, required)
	}
	return missing
}

func compactTrustedPluginAllowlistPaths(paths []string) []string {
	seen := map[string]bool{}
	var compact []string
	for _, candidate := range paths {
		trimmed := strings.TrimSpace(candidate)
		normalized := normalizeTrustedPluginPath(trimmed)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		compact = append(compact, trimmed)
	}
	sort.Slice(compact, func(i, j int) bool {
		return normalizeTrustedPluginPath(compact[i]) < normalizeTrustedPluginPath(compact[j])
	})
	return compact
}

func trustedPluginAllowlistRequiredPaths(repoDir string, host mayaHostConfig, payload []manifestPayload) ([]string, error) {
	root := trustedPluginArtifactsRoot(host)
	if root == "" {
		return nil, nil
	}
	required := []string{root}
	validationPaths := []string{root}
	for _, item := range payload {
		if item.Kind != "pluginArtifacts" {
			continue
		}
		destination := trustedPluginArtifactPath(host, item)
		if destination == "" {
			continue
		}
		validationPaths = append(validationPaths, destination)
		if err := validatePayloadPathForTransport(repoDir, item.Source); err != nil {
			return nil, fmt.Errorf("inspect Plugin Artifact %s: %w", item.Source, err)
		}
		sourcePath := filepath.Join(repoDir, filepath.FromSlash(item.Source))
		info, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("inspect Plugin Artifact %s: %w", item.Source, err)
		}
		if !info.IsDir() {
			required = append(required, remoteDir(destination))
			continue
		}
		required = append(required, destination)
		err = filepath.WalkDir(sourcePath, func(localPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relativePath, err := filepath.Rel(sourcePath, localPath)
			if err != nil {
				return err
			}
			if relativePath != "." {
				validationPaths = append(validationPaths, remoteJoin(destination, filepath.ToSlash(relativePath)))
			}
			if entry.IsDir() {
				return nil
			}
			pluginFile, err := isMayaPluginFile(localPath)
			if err != nil || !pluginFile {
				return err
			}
			relativeParent, err := filepath.Rel(sourcePath, filepath.Dir(localPath))
			if err != nil || relativeParent == "." {
				return err
			}
			required = append(required, remoteJoin(destination, filepath.ToSlash(relativeParent)))
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("inspect Plugin Artifact %s: %w", item.Source, err)
		}
	}
	for _, destination := range validationPaths {
		displayPath := trustedPluginValidationDisplayPath(root, destination)
		if err := rejectSFTPBatchUnsafePath(destination); err != nil {
			return nil, fmt.Errorf("inspect trusted Plugin Artifact path %q: %w", displayPath, err)
		}
		if hasWin32TrimmedPathComponent(destination) {
			return nil, fmt.Errorf("inspect trusted Plugin Artifact path %q: Windows path components ending in a space or period are not allowed", displayPath)
		}
		if hasInvalidWin32PathComponent(destination) {
			return nil, fmt.Errorf("inspect trusted Plugin Artifact path %q: invalid Windows path component", displayPath)
		}
	}
	return compactTrustedPluginAllowlistPaths(required), nil
}

func trustedPluginValidationDisplayPath(root string, destination string) string {
	normalizedRoot := strings.TrimRight(strings.ReplaceAll(root, `\`, "/"), "/")
	normalizedDestination := strings.ReplaceAll(destination, `\`, "/")
	if strings.EqualFold(normalizedDestination, normalizedRoot) {
		return "configured root"
	}
	prefix := normalizedRoot + "/"
	if len(normalizedDestination) >= len(prefix) && strings.EqualFold(normalizedDestination[:len(prefix)], prefix) {
		return normalizedDestination[len(prefix):]
	}
	return "configured destination"
}

func isMayaPluginFile(localPath string) (bool, error) {
	switch strings.ToLower(filepath.Ext(localPath)) {
	case ".mll", ".py":
		// Python can publish Maya callbacks dynamically, so syntax inspection
		// cannot safely distinguish plug-ins from helper modules.
		return true, nil
	default:
		return false, nil
	}
}

func parseSafeModeAllowedlistPaths(content string) []string {
	var paths []string
	for _, match := range safeModeAllowedlistOptionPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		switch match[1] {
		case "sa":
			paths = nil
		case "sva":
			if len(match) >= 3 {
				paths = append(paths, unescapeMELString(match[2]))
			}
		}
	}
	return paths
}

func unescapeMELString(value string) string {
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\`, `\`)
	return value
}

func normalizeTrustedPluginPath(value string) string {
	if clean, _, absolute, traversesRoot := canonicalWindowsPathForComparison(value); absolute && !traversesRoot {
		return clean
	}
	clean := path.Clean(strings.ReplaceAll(remotePath(value), `\`, "/"))
	if clean == "." {
		return ""
	}
	return strings.TrimRight(strings.ToLower(clean), "/")
}

func manifestHasPluginArtifacts(payload []manifestPayload) bool {
	for _, item := range payload {
		if item.Kind == "pluginArtifacts" {
			return true
		}
	}
	return false
}

func trustedPluginAllowlistMayaVersions(host mayaHostConfig, scenario scenarioConfig) []string {
	if strings.TrimSpace(scenario.MayaVersion) != "" {
		return []string{strings.TrimSpace(scenario.MayaVersion)}
	}
	if len(host.MayaVersions) > 0 {
		return compactMayaVersions(host.MayaVersions)
	}
	return nil
}

func compactMayaVersions(versions []string) []string {
	var compact []string
	for _, version := range versions {
		if trimmed := strings.TrimSpace(version); trimmed != "" {
			compact = append(compact, trimmed)
		}
	}
	return compact
}

func trustedPluginPrefsReadScript(host mayaHostConfig, version string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
%s
$prefs = MayaStall-PrefsPath %s
$exists = Test-Path -LiteralPath $prefs
$content = [string]''
if ($exists) {
  $content = [string](Get-Content -LiteralPath $prefs -Raw)
}
$json = [pscustomobject]@{ exists = $exists; content = $content } | ConvertTo-Json -Compress
Write-Output (%s + $json)
`, trustedPluginPrefsScriptPreamble(host), powerShellSingleQuoted(version), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix))
}

func trustedPluginPrefsRepairScript(host mayaHostConfig, version string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
%s
$prefs = MayaStall-PrefsPath %s
$requiredPathsJSON = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($MayaStallRequiredPathsInput))
$requiredPaths = @($requiredPathsJSON | ConvertFrom-Json | ForEach-Object { $_ })
function Normalize-MayaStallPath([string]$value) {
  try {
    return ([System.IO.Path]::GetFullPath(($value -replace '/','\')) -replace '\\','/').TrimEnd('/').ToLowerInvariant()
  } catch {
    return (($value -replace '\\','/').TrimEnd('/')).ToLowerInvariant()
  }
}
function Escape-MelString([string]$value) {
  return (($value -replace '\\','\\') -replace '"','\"')
}
$exists = Test-Path -LiteralPath $prefs
$content = [string]''
if ($exists) {
  $content = [string](Get-Content -LiteralPath $prefs -Raw)
} else {
  $json = [pscustomobject]@{ exists = $false; content = $content; changed = $false } | ConvertTo-Json -Compress
  Write-Output (%s + $json)
  return
}
$paths = New-Object System.Collections.Generic.List[string]
$normalizedPaths = New-Object System.Collections.Generic.HashSet[string]
foreach ($match in [regex]::Matches($content, '-(sva|sa)\s+"SafeModeAllowedlistPaths"(?:\s+"((?:\\.|[^"])*)")?')) {
  if ($match.Groups[1].Value -eq 'sa') {
    $paths.Clear()
    $normalizedPaths.Clear()
  } else {
    $entry = (($match.Groups[2].Value -replace '\\"','"') -replace '\\\\','\')
    if ($entry -and $normalizedPaths.Add((Normalize-MayaStallPath $entry))) {
      $paths.Add($entry)
    }
  }
}
$changed = $false
foreach ($requiredPath in $requiredPaths) {
  $wanted = Normalize-MayaStallPath $requiredPath
  if ($normalizedPaths.Add($wanted)) {
    $paths.Add($requiredPath)
    $changed = $true
  }
}
if ($changed) {
  if ($exists) {
    $stamp = Get-Date -Format 'yyyyMMddTHHmmss'
    Copy-Item -LiteralPath $prefs -Destination ($prefs + '.maya-stall-' + $stamp + '.bak') -Force
  }
  $nl = [Environment]::NewLine
  $block = New-Object System.Text.StringBuilder
  [void]$block.Append($nl)
  [void]$block.Append('// Maya Stall trusted Plugin Artifact destinations')
  [void]$block.Append($nl)
  [void]$block.Append('optionVar -cat "Security"')
  [void]$block.Append($nl)
  [void]$block.Append(' -sa "SafeModeAllowedlistPaths"')
  foreach ($entry in $paths) {
    [void]$block.Append($nl)
    [void]$block.Append(' -sva "SafeModeAllowedlistPaths" "')
    [void]$block.Append((Escape-MelString $entry))
    [void]$block.Append('"')
  }
  [void]$block.Append(';')
  [void]$block.Append($nl)
  Add-Content -LiteralPath $prefs -Value $block.ToString() -Encoding UTF8
}
$content = [string](Get-Content -LiteralPath $prefs -Raw)
$json = [pscustomobject]@{ exists = $true; content = $content; changed = $changed } | ConvertTo-Json -Compress
Write-Output (%s + $json)
`, trustedPluginPrefsScriptPreamble(host), powerShellSingleQuoted(version), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix))
}

func trustedPluginPrefsRepairInput(requiredPaths []string) (string, error) {
	data, err := json.Marshal(compactTrustedPluginAllowlistPaths(requiredPaths))
	if err != nil {
		return "", fmt.Errorf("encode trusted Plugin Artifact repair paths: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func trustedPluginPrefsRepairEnvelope(script string, requiredPathsInput string) (string, error) {
	data, err := json.Marshal(struct {
		Program       string `json:"program"`
		RequiredPaths string `json:"requiredPaths"`
	}{
		Program:       base64.StdEncoding.EncodeToString([]byte(script)),
		RequiredPaths: requiredPathsInput,
	})
	if err != nil {
		return "", fmt.Errorf("encode trusted Plugin Artifact repair program: %w", err)
	}
	return string(data), nil
}

func trustedPluginPrefsRepairBootstrapScript() string {
	return `$ErrorActionPreference = 'Stop'
$payload = [Console]::In.ReadToEnd() | ConvertFrom-Json
$MayaStallRequiredPathsInput = [string]$payload.requiredPaths
$program = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String([string]$payload.program))
& ([ScriptBlock]::Create($program))
`
}

func trustedPluginMayaAppDir(host mayaHostConfig) string {
	if host.Broker.isGGMayaSessiond() && strings.TrimSpace(host.Broker.StateDir) != "" {
		stateDir := strings.TrimSpace(host.Broker.StateDir)
		_, _, absolute, traversesRoot := canonicalWindowsPathForComparison(stateDir)
		if absolute && !traversesRoot {
			return remoteJoin(stateDir, "maya_app")
		}
		if repo := strings.TrimSpace(host.Broker.Repo); repo != "" {
			return remoteJoin(repo, stateDir, "maya_app")
		}
		return remoteJoin(stateDir, "maya_app")
	}
	return ""
}

func trustedPluginPrefsScriptPreamble(host mayaHostConfig) string {
	mayaAppDir := trustedPluginMayaAppDir(host)
	if mayaAppDir != "" {
		return fmt.Sprintf(`function MayaStall-PrefsPath([string]$version) {
  $mayaAppDir = %s
  return Join-Path (Join-Path $mayaAppDir $version) 'prefs\userPrefs.mel'
}
`, powerShellSingleQuoted(mayaAppDir))
	}
	return `function MayaStall-PrefsPath([string]$version) {
  $mayaAppDir = $env:MAYA_APP_DIR
  if ([string]::IsNullOrWhiteSpace($mayaAppDir)) {
    $mayaAppDir = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'maya'
  }
  return Join-Path (Join-Path $mayaAppDir $version) 'prefs\userPrefs.mel'
}
`
}
