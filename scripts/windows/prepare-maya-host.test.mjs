import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync, spawnSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const scriptPath = path.join(root, "scripts", "windows", "prepare-maya-host.ps1");
const script = fs.readFileSync(scriptPath, "utf8");
const fixturePython = pythonFixture();

test("prepare script exposes safe host-admin modes", () => {
  for (const flag of ["$CheckOnly", "$Force", "$Json", "$SkipSessiondInstall", "$NoStartTask"]) {
    assert.match(script, new RegExp(escapeRegExp(flag)));
  }
  assert.match(script, /existing launcher is not marked as Maya Stall generated/);
  assert.match(script, /rerun with -Force to replace it/);
  assert.match(script, /Normalize-LauncherContent/);
  assert.match(script, /Test-LauncherCanChange/);
  assert.match(script, /Invoke-CheckedNativeCommand/);
  assert.match(script, /Start-Process/);
  assert.match(script, /RedirectStandardOutput/);
  assert.match(script, /Quote-NativeArgument/);
  assert.match(script, /UTF8Encoding/);
  assert.match(script, /if \(-not \$Json\)/);
  assert.doesNotMatch(script, /C:\\PROJECTS\\GG/);
});

test("apply mode validates launcher replacement before venv but writes launcher after venv", () => {
  const preflightIndex = script.indexOf("$launcherCanChange = Test-LauncherCanChange");
  const applyBlockIndex = script.indexOf("} else {\n        if ($Ready) {", preflightIndex);
  const applyVenvIndex = script.indexOf("Ensure-Venv", applyBlockIndex);
  const applyLauncherIndex = script.indexOf("Ensure-Launcher $LauncherPath $launcherContent", applyVenvIndex);
  assert.ok(preflightIndex > -1, "launcher replacement preflight should be present");
  assert.ok(applyBlockIndex > preflightIndex, "apply mode branch should follow launcher preflight");
  assert.ok(applyVenvIndex > preflightIndex, "apply mode should create/install venv after launcher preflight");
  assert.ok(applyLauncherIndex > applyVenvIndex, "apply mode should write launcher after venv succeeds");
});

test("prepare script creates the expected work-root layout", () => {
  for (const segment of ["runs", "artifacts", "sessiond-ui", "sessiond-venv311", "start-sessiond-ui.cmd"]) {
    assert.match(script, new RegExp(escapeRegExp(segment)));
  }
  assert.match(script, /Ensure-Directory \$RunRoot "run-root"/);
  assert.match(script, /Ensure-Directory \$ArtifactRoot "artifact-root"/);
  assert.match(script, /Ensure-Directory \$StateDir "sessiond-state"/);
});

test("prepare script verifies the advertised Python capability", () => {
  assert.match(script, /sys\.version_info\.micro/);
  assert.match(script, /Test-VersionPrefix/);
  assert.match(script, /could not query source Python version/);
  assert.match(script, /source Python \$sourcePythonVersion does not match declared capability/);
  assert.match(script, /does not match declared capability/);
});

test("generated launcher starts gg_mayasessiond with UI broker paths", () => {
  for (const token of [
    "-m gg_maya_sessiond.cli start",
    "--state-dir \"%SESSIOND_STATE%\"",
    "--maya-exe \"%MAYA_EXE%\"",
    "--mcp-python \"%SESSIOND_PYTHON%\"",
    "--mcp-src \"%MCP_SRC%\"",
    "--mcp-script-dirs \"%MAYA_STALL_RUNS%\"",
    "--wait-timeout-seconds $WaitTimeoutSeconds",
    "--json",
  ]) {
    assert.match(script, new RegExp(escapeRegExp(token)));
  }
});

test("scheduled task command is interactive and idempotent", () => {
  const taskArgs = script.match(/return @\(([^;]+?)\)\r?\n}\r?\n\r?\nfunction Ensure-ScheduledTask/s)?.[1] ?? "";
  for (const token of ["/Create", "/TN", "/TR", "/SC", "ONLOGON", "/RL", "HIGHEST", "/IT", "/F"]) {
    assert.match(taskArgs, new RegExp(escapeRegExp(token)));
  }
  assert.match(script, /would run: schtasks\.exe/);
  assert.match(script, /created or updated interactive scheduled task/);
  assert.match(script, /would start interactive scheduled task/);
  assert.match(script, /started interactive scheduled task/);
  assert.match(script, /required paths are missing; not mutating virtual environment, launcher, or scheduled task/);
  assert.match(script, /launcher is blocked; not creating or updating scheduled task/);
});

test("host-config snippet stays public and points doctor at the prepared host", () => {
  for (const token of [
    "host_config_yaml:",
    "${TargetProfile}:",
    "${HostPool}:",
    "transport: ssh",
    "sftpTimeout: $SftpTimeout",
    "type: gg-mayasessiond",
    "visualEvidence: true",
    "mayaBuilds: [\"$SessionMayaBuild\"]",
    "sessionMayaBuild: \"$SessionMayaBuild\"",
    "python: \"$ReportedPythonVersion\"",
    "features: [script.execute]",
    "trustedPluginArtifacts: false",
    "maya-stall doctor --host-config <host-config.yaml> --target-profile $TargetProfile --host $HostId --scenario smoke",
  ]) {
    assert.match(script, new RegExp(escapeRegExp(token)));
  }
});

test("check-only fixture reports planned host shape without mutation when pwsh and Python are available", { skip: !hasCommand("pwsh") || !fixturePython }, () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "maya-stall-prepare-"));
  const sessiondRepo = path.join(dir, "GG_MayaSessiond");
  const mcpSource = path.join(dir, "GG_MayaMCP");
  const mayaExe = path.join(dir, "maya.exe");
  const workRoot = path.join(dir, "maya-stall");
  const venvPath = path.join(dir, "venv");
  const launcherPath = path.join(dir, "start-sessiond-ui.cmd");
  fs.mkdirSync(sessiondRepo);
  fs.mkdirSync(mcpSource);
  fs.writeFileSync(mayaExe, "");

  const probe = spawnSync("pwsh", [
    "-NoProfile",
    "-File",
    scriptPath,
    "-CheckOnly",
    "-Json",
    "-SkipSessiondInstall",
    "-WorkRoot",
    workRoot,
    "-VenvPath",
    venvPath,
    "-LauncherPath",
    launcherPath,
    "-SessiondRepo",
    sessiondRepo,
    "-McpSource",
    mcpSource,
    "-MayaExe",
    mayaExe,
    "-SessionMayaBuild",
    "2025.3",
    "-PythonVersion",
    fixturePython.version,
    "-PythonForVenv",
    fixturePython.executable,
    "-PythonForVenvArgs",
    ...fixturePython.args,
    "-HostId",
    "maya-win-fixture",
    "-TargetProfile",
    "ci",
    "-SshHost",
    "maya-win-fixture",
    "-SshUser",
    "maya-runner",
  ], { cwd: root, encoding: "utf8" });

  assert.equal(probe.status, 0, probe.stderr);
  const result = JSON.parse(probe.stdout);
  assert.equal(result.ready, true);
  assert.equal(result.checkOnly, true);
  assert.equal(fs.existsSync(workRoot), false);
  assert.equal(fs.existsSync(launcherPath), false);
  assert.deepEqual(result.plan.filter((step) => step.status === "planned").map((step) => step.kind), [
    "work-root",
    "run-root",
    "artifact-root",
    "sessiond-state",
    "launcher",
    "python-venv",
    "scheduled-task",
    "scheduled-task-start",
  ]);
});

test("check-only fixture keeps dependent plan visible when a manual prerequisite is missing", { skip: !hasCommand("pwsh") || !fixturePython }, () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "maya-stall-prepare-missing-"));
  const sessiondRepo = path.join(dir, "GG_MayaSessiond");
  const mcpSource = path.join(dir, "GG_MayaMCP");
  const mayaExe = path.join(dir, "maya.exe");
  const workRoot = path.join(dir, "maya-stall");
  const venvPath = path.join(dir, "venv");
  const launcherPath = path.join(dir, "start-sessiond-ui.cmd");
  fs.mkdirSync(sessiondRepo);
  fs.writeFileSync(mayaExe, "");

  const probe = spawnSync("pwsh", [
    "-NoProfile",
    "-File",
    scriptPath,
    "-CheckOnly",
    "-Json",
    "-SkipSessiondInstall",
    "-WorkRoot",
    workRoot,
    "-VenvPath",
    venvPath,
    "-LauncherPath",
    launcherPath,
    "-SessiondRepo",
    sessiondRepo,
    "-McpSource",
    mcpSource,
    "-MayaExe",
    mayaExe,
    "-SessionMayaBuild",
    "2025.3",
    "-PythonVersion",
    fixturePython.version,
    "-PythonForVenv",
    fixturePython.executable,
    "-PythonForVenvArgs",
    ...fixturePython.args,
    "-HostId",
    "maya-win-fixture",
    "-TargetProfile",
    "ci",
    "-SshHost",
    "maya-win-fixture",
    "-SshUser",
    "maya-runner",
  ], { cwd: root, encoding: "utf8" });

  assert.equal(probe.status, 1, probe.stderr);
  const result = JSON.parse(probe.stdout);
  assert.equal(result.ready, false);
  const byKind = new Map(result.plan.map((step) => [step.kind, step]));
  assert.equal(byKind.get("GG_MayaMCP")?.status, "missing");
  assert.equal(byKind.get("launcher")?.status, "planned");
  assert.equal(byKind.get("python-venv")?.status, "planned");
  assert.equal(byKind.get("scheduled-task")?.status, "planned");
  assert.equal(byKind.get("scheduled-task-start")?.status, "planned");
});

function hasCommand(command) {
  const result = spawnSync(command, ["-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"], {
    encoding: "utf8",
    stdio: "pipe",
  });
  return result.status === 0;
}

function pythonFixture() {
  const windows = process.platform === "win32";
  const command = windows ? "py" : "python3";
  const args = windows ? ["-3.11"] : [];
  const probe = spawnSync(command, [...args, "-c", "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')"], {
    encoding: "utf8",
    stdio: "pipe",
  });
  if (probe.status !== 0 || !probe.stdout.trim()) return null;
  return {
    executable: windows ? command : "/usr/bin/env",
    args: windows ? args : [command],
    version: probe.stdout.trim(),
  };
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
