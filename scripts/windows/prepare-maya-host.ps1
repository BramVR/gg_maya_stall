<#
.SYNOPSIS
Prepares an already-licensed Windows machine as a Maya Stall Maya Host.

.DESCRIPTION
Creates or verifies the Maya Stall work-root layout, a Python virtual
environment for gg_mayasessiond, a UI Session Broker launcher, and an
interactive scheduled task. The script does not install Maya, configure SSH,
create Windows users, store credentials, or write repo config.
#>
[CmdletBinding()]
param(
    [string]$WorkRoot = "C:\maya-stall",
    [string]$SessiondRepo = "C:\maya-stall-src\GG_MayaSessiond",
    [string]$McpSource = "C:\maya-stall-src\GG_MayaMCP",
    [Parameter(Mandatory = $true)]
    [string]$MayaExe,
    [string]$VenvPath,
    [string]$PythonForVenv = "py",
    [string[]]$PythonForVenvArgs = @("-3.11"),
    [string]$TaskName = "MayaStallSessiondUI",
    [string]$LauncherPath,
    [string]$HostId = "maya-win-01",
    [string]$TargetProfile = "ci",
    [string]$HostPool = "windows-maya",
    [string]$SshHost = "maya-win-01",
    [string]$SshUser = "maya-runner",
    [int]$SshPort = 22,
    [string]$IdentityFile = "~/.ssh/maya-stall-ci",
    [string]$SftpTimeout = "30m",
    [string]$MayaVersion = "2025",
    [int]$WaitTimeoutSeconds = 180,
    [switch]$CheckOnly,
    [switch]$SkipSessiondInstall,
    [switch]$NoStartTask,
    [switch]$Force,
    [switch]$Json
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = "Stop"

function Join-MayaStallPath {
    param([string]$Base, [string]$Child)
    $cleanBase = ($Base.Trim() -replace '/', '\').TrimEnd('\')
    $cleanChild = ($Child.Trim() -replace '/', '\').TrimStart('\')
    return "$cleanBase\$cleanChild"
}

if (-not $VenvPath) {
    $VenvPath = Join-MayaStallPath $WorkRoot "sessiond-venv311"
}
if (-not $LauncherPath) {
    $LauncherPath = Join-MayaStallPath $WorkRoot "start-sessiond-ui.cmd"
}

$RunRoot = Join-MayaStallPath $WorkRoot "runs"
$ArtifactRoot = Join-MayaStallPath $WorkRoot "artifacts"
$StateDir = Join-MayaStallPath $WorkRoot "sessiond-ui"
$VenvPython = Join-MayaStallPath $VenvPath "Scripts\python.exe"
$Plan = New-Object System.Collections.Generic.List[object]
$Ready = $true
$Marker = "Maya Stall generated UI Session Broker launcher"

function ConvertTo-HostConfigPath {
    param([string]$Path)
    return ($Path.Trim() -replace '\\', '/')
}

function Add-PrepareStep {
    param(
        [string]$Kind,
        [string]$Path,
        [string]$Status,
        [string]$Detail,
        [bool]$IsReady = $true
    )
    $script:Plan.Add([pscustomobject]@{
        kind = $Kind
        path = $Path
        status = $Status
        detail = $Detail
        ready = $IsReady
    }) | Out-Null
    if (-not $IsReady) {
        $script:Ready = $false
    }
}

function ConvertTo-NativeArgumentString {
    param([string[]]$Arguments)
    return (($Arguments | ForEach-Object { Quote-NativeArgument ([string]$_) }) -join " ")
}

function Quote-NativeArgument {
    param([string]$Argument)
    if ($Argument -ne "" -and $Argument -notmatch '[\s"]') {
        return $Argument
    }
    $result = '"'
    $backslashes = 0
    foreach ($character in $Argument.ToCharArray()) {
        $text = [string]$character
        if ($text -eq '\') {
            $backslashes++
            continue
        }
        if ($text -eq '"') {
            $result += ('\' * (($backslashes * 2) + 1))
            $result += '"'
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            $result += ('\' * $backslashes)
            $backslashes = 0
        }
        $result += $text
    }
    if ($backslashes -gt 0) {
        $result += ('\' * ($backslashes * 2))
    }
    $result += '"'
    return $result
}

function Invoke-CheckedNativeCommand {
    param([string]$Label, [string]$FilePath, [string[]]$Arguments)
    $stdoutPath = [System.IO.Path]::GetTempFileName()
    $stderrPath = [System.IO.Path]::GetTempFileName()
    try {
        $process = Start-Process -FilePath $FilePath -ArgumentList (ConvertTo-NativeArgumentString $Arguments) -NoNewWindow -Wait -PassThru -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath
        $exitCode = $process.ExitCode
        $stdout = Get-Content -LiteralPath $stdoutPath -Raw -ErrorAction SilentlyContinue
        $stderr = Get-Content -LiteralPath $stderrPath -Raw -ErrorAction SilentlyContinue
    } finally {
        Remove-Item -LiteralPath $stdoutPath, $stderrPath -Force -ErrorAction SilentlyContinue
    }

    if (-not $Json) {
        if ($stdout) { [Console]::Out.Write($stdout) }
        if ($stderr) { [Console]::Error.Write($stderr) }
    }
    if ($exitCode -ne $null -and $exitCode -ne 0) {
        $detail = (($stderr + $stdout).Trim())
        if ($detail) {
            throw "$Label failed with exit code ${exitCode}: $detail"
        }
        throw "$Label failed with exit code $exitCode"
    }
}

function Ensure-Directory {
    param([string]$Path, [string]$Label)
    if (Test-Path -LiteralPath $Path -PathType Container) {
        Add-PrepareStep $Label $Path "ok" "directory exists" $true
        return
    }
    if ($CheckOnly) {
        Add-PrepareStep $Label $Path "planned" "would create directory" $true
        return
    }
    New-Item -ItemType Directory -Force -Path $Path | Out-Null
    Add-PrepareStep $Label $Path "changed" "created directory" $true
}

function Assert-ExistingPath {
    param([string]$Path, [string]$Label, [string]$PathType)
    if (Test-Path -LiteralPath $Path -PathType $PathType) {
        Add-PrepareStep $Label $Path "ok" "$PathType exists" $true
        return
    }
    Add-PrepareStep $Label $Path "missing" "$PathType is required; install or clone it outside repo config" $false
}

function New-LauncherContent {
    param(
        [string]$SessiondRepo,
        [string]$VenvPython,
        [string]$StateDir,
        [string]$MayaExe,
        [string]$McpSource,
        [string]$RunRoot,
        [int]$WaitTimeoutSeconds
    )
    return @"
@echo off
rem $Marker
setlocal
set "SESSIOND_REPO=$SessiondRepo"
set "SESSIOND_PYTHON=$VenvPython"
set "SESSIOND_STATE=$StateDir"
set "MAYA_EXE=$MayaExe"
set "MCP_SRC=$McpSource"
set "MAYA_STALL_RUNS=$RunRoot"
cd /d "%SESSIOND_REPO%"
"%SESSIOND_PYTHON%" -m gg_maya_sessiond.cli start --state-dir "%SESSIOND_STATE%" --maya-exe "%MAYA_EXE%" --mcp-python "%SESSIOND_PYTHON%" --mcp-src "%MCP_SRC%" --mcp-script-dirs "%MAYA_STALL_RUNS%" --wait-timeout-seconds $WaitTimeoutSeconds --json
"@
}

function Normalize-LauncherContent {
    param([string]$Content)
    return (($Content -replace "`r`n", "`n").TrimEnd() + "`n")
}

function Ensure-Launcher {
    param([string]$Path, [string]$Content)
    $existing = $null
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        $existing = Get-Content -LiteralPath $Path -Raw
        if ((Normalize-LauncherContent $existing) -eq (Normalize-LauncherContent $Content)) {
            Add-PrepareStep "launcher" $Path "ok" "launcher is current" $true
            return
        }
        if (($existing -notlike "*$Marker*") -and (-not $Force)) {
            Add-PrepareStep "launcher" $Path "blocked" "existing launcher is not marked as Maya Stall generated; rerun with -Force to replace it" $false
            return
        }
    }
    if ($CheckOnly) {
        Add-PrepareStep "launcher" $Path "planned" "would write UI Session Broker launcher" $true
        return
    }
    $parent = Split-Path -Parent $Path
    if ($parent) {
        New-Item -ItemType Directory -Force -Path $parent | Out-Null
    }
    $encoding = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($Path, $Content, $encoding)
    Add-PrepareStep "launcher" $Path "changed" "wrote UI Session Broker launcher" $true
}

function Test-LauncherCanChange {
    param([string]$Path, [string]$Content)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return $true
    }
    $existing = Get-Content -LiteralPath $Path -Raw
    if ((Normalize-LauncherContent $existing) -eq (Normalize-LauncherContent $Content)) {
        return $true
    }
    if (($existing -notlike "*$Marker*") -and (-not $Force)) {
        Add-PrepareStep "launcher" $Path "blocked" "existing launcher is not marked as Maya Stall generated; rerun with -Force to replace it" $false
        return $false
    }
    return $true
}

function Ensure-Venv {
    if (Test-Path -LiteralPath $VenvPython -PathType Leaf) {
        Add-PrepareStep "python-venv" $VenvPath "ok" "virtual environment exists" $true
    } elseif ($CheckOnly) {
        Add-PrepareStep "python-venv" $VenvPath "planned" "would create virtual environment" $true
    } else {
        New-Item -ItemType Directory -Force -Path $WorkRoot | Out-Null
        Invoke-CheckedNativeCommand "create Python virtual environment" $PythonForVenv ($PythonForVenvArgs + @("-m", "venv", $VenvPath))
        Add-PrepareStep "python-venv" $VenvPath "changed" "created virtual environment" $true
    }

    if ($CheckOnly -or $SkipSessiondInstall) {
        return
    }
    Invoke-CheckedNativeCommand "install gg_mayasessiond" $VenvPython @("-m", "pip", "install", "-e", $SessiondRepo)
    Add-PrepareStep "python-venv" $VenvPath "changed" "installed gg_mayasessiond into virtual environment" $true
}

function New-ScheduledTaskArgs {
    param([string]$TaskName, [string]$LauncherPath)
    $taskRun = '"' + $LauncherPath + '"'
    return @("/Create", "/TN", $TaskName, "/TR", $taskRun, "/SC", "ONLOGON", "/RL", "HIGHEST", "/IT", "/F")
}

function Ensure-ScheduledTask {
    $taskArgs = New-ScheduledTaskArgs $TaskName $LauncherPath
    if ($CheckOnly) {
        Add-PrepareStep "scheduled-task" $TaskName "planned" ("would run: schtasks.exe " + ($taskArgs -join " ")) $true
        if (-not $NoStartTask) {
            Add-PrepareStep "scheduled-task-start" $TaskName "planned" "would start interactive scheduled task" $true
        }
        return
    }
    Invoke-CheckedNativeCommand "create scheduled task" "schtasks.exe" $taskArgs
    Add-PrepareStep "scheduled-task" $TaskName "changed" "created or updated interactive scheduled task" $true
    if (-not $NoStartTask) {
        Invoke-CheckedNativeCommand "start scheduled task" "schtasks.exe" @("/Run", "/TN", $TaskName)
        Add-PrepareStep "scheduled-task-start" $TaskName "changed" "started interactive scheduled task" $true
    }
}

function Write-HostConfigSnippet {
    $workRootYaml = ConvertTo-HostConfigPath $WorkRoot
    $stateDirYaml = ConvertTo-HostConfigPath $StateDir
    $venvPythonYaml = ConvertTo-HostConfigPath $VenvPython
    $sessiondRepoYaml = ConvertTo-HostConfigPath $SessiondRepo
    $mcpSourceYaml = ConvertTo-HostConfigPath $McpSource

    @"
host_config_yaml:
version: 1
targetProfiles:
  ${TargetProfile}:
    hostPool: $HostPool
hostPools:
  ${HostPool}:
    hosts:
      - id: $HostId
        transport: ssh
        ssh:
          host: $SshHost
          user: $SshUser
          port: $SshPort
          identityFile: $IdentityFile
          sftpTimeout: $SftpTimeout
        workRoot: $workRootYaml
        broker:
          type: gg-mayasessiond
          stateDir: $stateDirYaml
          python: $venvPythonYaml
          repo: $sessiondRepoYaml
          mcpSource: $mcpSourceYaml
        mayaVersions: ["$MayaVersion"]
        visualEvidence: true

doctor_command:
maya-stall doctor --host-config <host-config.yaml> --target-profile $TargetProfile --host $HostId --scenario smoke
"@
}

Ensure-Directory $WorkRoot "work-root"
Ensure-Directory $RunRoot "run-root"
Ensure-Directory $ArtifactRoot "artifact-root"
Ensure-Directory $StateDir "sessiond-state"
Assert-ExistingPath $SessiondRepo "gg_mayasessiond" "Container"
Assert-ExistingPath $McpSource "GG_MayaMCP" "Container"
Assert-ExistingPath $MayaExe "maya-exe" "Leaf"
if ((-not $Ready) -and (-not $CheckOnly)) {
    Add-PrepareStep "dependent-setup" $WorkRoot "skipped" "required paths are missing; not mutating virtual environment, launcher, or scheduled task" $false
} else {
    $launcherContent = New-LauncherContent $SessiondRepo $VenvPython $StateDir $MayaExe $McpSource $RunRoot $WaitTimeoutSeconds
    $launcherCanChange = Test-LauncherCanChange $LauncherPath $launcherContent
    if ($CheckOnly) {
        if (-not $launcherCanChange) {
            Add-PrepareStep "scheduled-task" $TaskName "skipped" "launcher is blocked; not creating or updating scheduled task" $false
        } else {
            Ensure-Launcher $LauncherPath $launcherContent
            Ensure-Venv
            Ensure-ScheduledTask
        }
    } else {
        if ($Ready) {
            Ensure-Venv
        }
        if ($Ready) {
            Ensure-Launcher $LauncherPath $launcherContent
        }
        if ($Ready) {
            Ensure-ScheduledTask
        } else {
            Add-PrepareStep "scheduled-task" $TaskName "skipped" "launcher is blocked; not creating or updating scheduled task" $false
        }
    }
}

$result = [pscustomobject]@{
    ready = $Ready
    checkOnly = [bool]$CheckOnly
    workRoot = $WorkRoot
    runRoot = $RunRoot
    artifactRoot = $ArtifactRoot
    stateDir = $StateDir
    venvPython = $VenvPython
    launcherPath = $LauncherPath
    taskName = $TaskName
    plan = $Plan
}

if ($Json) {
    $result | ConvertTo-Json -Depth 6
} else {
    foreach ($step in $Plan) {
        Write-Output ("{0}: {1}: {2} ({3})" -f $step.kind, $step.status, $step.path, $step.detail)
    }
    Write-Output ("ready: {0}" -f $Ready.ToString().ToLowerInvariant())
    Write-Output ""
    Write-Output (Write-HostConfigSnippet)
}

if (-not $Ready) {
    exit 1
}
