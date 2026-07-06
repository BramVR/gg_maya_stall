import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync, spawnSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const scriptPath = path.join(root, "scripts", "windows", "prepare-maya-host.ps1");
const script = fs.readFileSync(scriptPath, "utf8");

test("prepare script exposes safe host-admin modes", () => {
  for (const flag of ["$CheckOnly", "$Force", "$Json", "$SkipSessiondInstall", "$NoStartTask"]) {
    assert.match(script, new RegExp(escapeRegExp(flag)));
  }
  assert.match(script, /existing launcher is not marked as Maya Stall generated/);
  assert.match(script, /rerun with -Force to replace it/);
  assert.match(script, /Normalize-LauncherContent/);
  assert.match(script, /Invoke-CheckedNativeCommand/);
  assert.match(script, /Start-Process/);
  assert.match(script, /RedirectStandardOutput/);
  assert.match(script, /Quote-NativeArgument/);
  assert.match(script, /if \(-not \$Json\)/);
});

test("prepare script creates the expected work-root layout", () => {
  for (const segment of ["runs", "artifacts", "sessiond-ui", "sessiond-venv311", "start-sessiond-ui.cmd"]) {
    assert.match(script, new RegExp(escapeRegExp(segment)));
  }
  assert.match(script, /Ensure-Directory \$RunRoot "run-root"/);
  assert.match(script, /Ensure-Directory \$ArtifactRoot "artifact-root"/);
  assert.match(script, /Ensure-Directory \$StateDir "sessiond-state"/);
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
  const taskArgs = script.match(/return @\(([^;]+?)\)\n}\n\nfunction Ensure-ScheduledTask/s)?.[1] ?? "";
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
    "maya-stall doctor --host-config <host-config.yaml> --target-profile $TargetProfile --host $HostId --scenario smoke",
  ]) {
    assert.match(script, new RegExp(escapeRegExp(token)));
  }
});

test("check-only fixture reports planned host shape without mutation when pwsh is available", { skip: !hasCommand("pwsh") }, () => {
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

  const raw = execFileSync("pwsh", [
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
    "-HostId",
    "maya-win-fixture",
    "-TargetProfile",
    "ci",
    "-SshHost",
    "maya-win-fixture",
    "-SshUser",
    "maya-runner",
  ], { cwd: root, encoding: "utf8" });

  const result = JSON.parse(raw);
  assert.equal(result.ready, true);
  assert.equal(result.checkOnly, true);
  assert.equal(fs.existsSync(workRoot), false);
  assert.equal(fs.existsSync(launcherPath), false);
  assert.deepEqual(result.plan.filter((step) => step.status === "planned").map((step) => step.kind), [
    "work-root",
    "run-root",
    "artifact-root",
    "sessiond-state",
    "python-venv",
    "launcher",
    "scheduled-task",
    "scheduled-task-start",
  ]);
});

function hasCommand(command) {
  const result = spawnSync(command, ["-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()"], {
    encoding: "utf8",
    stdio: "pipe",
  });
  return result.status === 0;
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
