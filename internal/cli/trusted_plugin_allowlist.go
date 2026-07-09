package cli

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const trustedPluginAllowlistLayerID = "trusted-plugin-allowlist"
const safeModeAllowedlistOptionVar = "SafeModeAllowedlistPaths"
const trustedPluginPrefsJSONPrefix = "MAYA_STALL_TRUST_PREFS_JSON:"

var safeModeAllowedlistPathPattern = regexp.MustCompile(`(?m)-sva\s+"SafeModeAllowedlistPaths"\s+"((?:\\.|[^"])*)"`)
var mayaPrefsVersionPattern = regexp.MustCompile(`^[0-9]{4}(?:\.[0-9]+)?$`)

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

func trustedPluginAllowlistDoctorLayer(host mayaHostConfig, scenario scenarioConfig, repair bool, sshOK bool, lockClear bool) doctorCheck {
	root := trustedPluginArtifactsRoot(host)
	if root == "" {
		return withSource(okCheck(trustedPluginAllowlistLayerID, "not configured"), "config")
	}
	if err := validateTrustedPluginArtifactsRoot(host); err != nil {
		return withSource(failedCheck(trustedPluginAllowlistLayerID, err.Error(), "Choose a narrow trustedPluginArtifactsRoot outside workRoot/runs. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."), "config")
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
	versions := trustedPluginAllowlistMayaVersions(host, scenario)
	changed, err := ensureTrustedPluginAllowlist(host, versions, repair)
	if err != nil {
		return withSource(failedCheck(trustedPluginAllowlistLayerID, err.Error(), "Add trustedPluginArtifactsRoot to Maya's trusted plug-in locations, or run doctor with --repair-trusted-plugin-allowlist after approving this host policy change. See docs/setup/windows-maya-host.md#trusted-plugin-artifacts."), "maya-prefs")
	}
	detail := fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
	if repair && changed {
		detail = fmt.Sprintf("Maya %s %s contains trustedPluginArtifactsRoot after repair", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
	} else if repair {
		detail = fmt.Sprintf("Maya %s %s already contains trustedPluginArtifactsRoot", strings.Join(versions, ","), safeModeAllowedlistOptionVar)
	}
	return withSource(okCheck(trustedPluginAllowlistLayerID, detail), "maya-prefs")
}

func ensureTrustedPluginArtifactsAllowlistedForRun(host mayaHostConfig, scenario scenarioContract) error {
	if trustedPluginArtifactsRoot(host) == "" || !host.usesRealSSH() || !manifestHasPluginArtifacts(scenario.Payload) {
		return nil
	}
	if _, err := ensureTrustedPluginAllowlist(host, trustedPluginAllowlistMayaVersions(host, scenario.Config), false); err != nil {
		return fmt.Errorf("trusted Plugin Artifact allowlist preflight failed: %w", err)
	}
	return nil
}

func ensureTrustedPluginAllowlist(host mayaHostConfig, versions []string, repair bool) (bool, error) {
	if repair {
		sessions, err := mayaProcessSessions(host)
		if err != nil {
			return false, fmt.Errorf("check Maya process before TrustCenter repair: %w", err)
		}
		if len(sessions) > 0 {
			return false, fmt.Errorf("TrustCenter repair requires Maya to be stopped first so userPrefs.mel is not overwritten on exit")
		}
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
		probe, err := trustedPluginPrefs(host, version, repair)
		if err != nil {
			return false, fmt.Errorf("maya %s %s", version, err)
		}
		if !probe.Exists {
			return false, fmt.Errorf("maya %s userPrefs.mel is missing", version)
		}
		if !prefsAllowlistContainsRoot(probe.Content, trustedPluginArtifactsRoot(host)) {
			return false, fmt.Errorf("maya %s %s does not contain trustedPluginArtifactsRoot", version, safeModeAllowedlistOptionVar)
		}
		changed = changed || probe.Changed
	}
	if checked == 0 {
		return false, fmt.Errorf("maya version is required to locate TrustCenter preferences; set host mayaVersions or Scenario mayaVersion")
	}
	return changed, nil
}

func validateMayaPrefsVersion(version string) error {
	if !mayaPrefsVersionPattern.MatchString(version) {
		return fmt.Errorf("maya version %q is not a safe preferences path segment", version)
	}
	return nil
}

func trustedPluginPrefs(host mayaHostConfig, version string, repair bool) (trustedPluginPrefsProbe, error) {
	script := trustedPluginPrefsReadScript(version)
	if repair {
		script = trustedPluginPrefsRepairScript(version, trustedPluginArtifactsRoot(host))
	}
	raw, err := runSSHCommandOutput(host, encodedPowerShellCommand(script), sshCommandTimeout)
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
	want := normalizeTrustedPluginPath(root)
	if want == "" {
		return false
	}
	for _, entry := range parseSafeModeAllowedlistPaths(content) {
		if normalizeTrustedPluginPath(entry) == want {
			return true
		}
	}
	return false
}

func parseSafeModeAllowedlistPaths(content string) []string {
	var paths []string
	for _, match := range safeModeAllowedlistPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		paths = append(paths, unescapeMELString(match[1]))
	}
	return paths
}

func unescapeMELString(value string) string {
	value = strings.ReplaceAll(value, `\"`, `"`)
	value = strings.ReplaceAll(value, `\\`, `\`)
	return value
}

func normalizeTrustedPluginPath(value string) string {
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

func trustedPluginPrefsReadScript(version string) string {
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
`, trustedPluginPrefsScriptPreamble(), powerShellSingleQuoted(version), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix))
}

func trustedPluginPrefsRepairScript(version string, root string) string {
	return fmt.Sprintf(`$ErrorActionPreference = 'Stop'
%s
$prefs = MayaStall-PrefsPath %s
$root = %s
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
foreach ($match in [regex]::Matches($content, '-sva\s+"SafeModeAllowedlistPaths"\s+"((?:\\.|[^"])*)"')) {
  $entry = (($match.Groups[1].Value -replace '\\"','"') -replace '\\\\','\')
  if ($entry -and -not $paths.Contains($entry)) {
    $paths.Add($entry)
  }
}
$wanted = Normalize-MayaStallPath $root
$found = $false
foreach ($entry in $paths) {
  if ((Normalize-MayaStallPath $entry) -eq $wanted) {
    $found = $true
  }
}
$changed = $false
if (-not $found) {
  if ($exists) {
    $stamp = Get-Date -Format 'yyyyMMddTHHmmss'
    Copy-Item -LiteralPath $prefs -Destination ($prefs + '.maya-stall-' + $stamp + '.bak') -Force
  }
  $paths.Add($root)
  $nl = [Environment]::NewLine
  $block = $nl + '// Maya Stall trusted Plugin Artifact root' + $nl + 'optionVar -cat "Security"' + $nl + ' -sa "SafeModeAllowedlistPaths"'
  foreach ($entry in $paths) {
    $block += $nl + ' -sva "SafeModeAllowedlistPaths" "' + (Escape-MelString $entry) + '"'
  }
  $block += ';' + $nl
  Add-Content -LiteralPath $prefs -Value $block -Encoding UTF8
  $changed = $true
}
$content = [string](Get-Content -LiteralPath $prefs -Raw)
$json = [pscustomobject]@{ exists = $true; content = $content; changed = $changed } | ConvertTo-Json -Compress
Write-Output (%s + $json)
`, trustedPluginPrefsScriptPreamble(), powerShellSingleQuoted(version), powerShellSingleQuoted(root), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix), powerShellSingleQuoted(trustedPluginPrefsJSONPrefix))
}

func trustedPluginPrefsScriptPreamble() string {
	return `function MayaStall-PrefsPath([string]$version) {
  $mayaAppDir = $env:MAYA_APP_DIR
  if ([string]::IsNullOrWhiteSpace($mayaAppDir)) {
    $mayaAppDir = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'maya'
  }
  return Join-Path (Join-Path $mayaAppDir $version) 'prefs\userPrefs.mel'
}
`
}
