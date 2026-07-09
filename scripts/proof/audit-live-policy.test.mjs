import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const auditScript = path.join(root, "scripts", "proof", "audit-live-policy.mjs");

test("checked-in policy covers the live-sensitive surface of this repo", () => {
  const output = execFileSync("node", [auditScript], { cwd: root, encoding: "utf8" });
  assert.match(output, /live-policy-audit: ok/);
});

test("audit fails when a policy path no longer exists", () => {
  const dir = fixtureRepo({
    "internal/cli/run.go": "package cli\n",
  });
  const policyPath = writePolicy(dir, [
    { id: "renamed", reason: "renamed file", paths: ["internal/cli/gone.go"] },
    { id: "live-product-source", reason: "live source", prefixes: ["internal/cli/"] },
  ]);

  const result = runAudit(dir, policyPath);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /rule renamed: path internal\/cli\/gone\.go is not a tracked file/);
});

test("audit fails when a prefix matches no tracked files", () => {
  const dir = fixtureRepo({
    "internal/cli/run.go": "package cli\n",
  });
  const policyPath = writePolicy(dir, [
    { id: "live-product-source", reason: "live source", prefixes: ["internal/cli/", "scripts/windows/"] },
  ]);

  const result = runAudit(dir, policyPath);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /rule live-product-source: prefix scripts\/windows\/ matches no tracked files/);
});

test("audit fails when a live-sensitive file escapes every rule", () => {
  const dir = fixtureRepo({
    "internal/cli/run.go": "package cli\n",
    "internal/transport/ssh.go": "package transport\n",
  });
  const policyPath = writePolicy(dir, [
    { id: "live-product-source", reason: "live source", prefixes: ["internal/cli/"] },
  ]);

  const result = runAudit(dir, policyPath);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /live-sensitive file internal\/transport\/ssh\.go is not covered by any policy rule/);
});

test("audit passes for a covered fixture repo", () => {
  const dir = fixtureRepo({
    "internal/cli/run.go": "package cli\n",
    "cmd/maya-stall/main.go": "package main\n",
    "docs/readme.md": "docs are not live-sensitive\n",
  });
  const policyPath = writePolicy(dir, [
    { id: "live-product-source", reason: "live source", prefixes: ["internal/cli/", "cmd/"] },
  ]);

  const result = runAudit(dir, policyPath);
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, /live-policy-audit: ok/);
});

test("audit reports unknown options without a stack trace", () => {
  const result = runAuditArgs(["--unknown"]);
  assert.equal(result.code, 2);
  assert.equal(result.stderr, "live-policy-audit: unknown option --unknown\n");
});

test("audit reports missing option values without a stack trace", () => {
  const result = runAuditArgs(["--root"]);
  assert.equal(result.code, 2);
  assert.equal(result.stderr, "live-policy-audit: --root needs a value\n");
});

function runAudit(dir, policyPath) {
  return runAuditArgs(["--root", dir, "--policy", policyPath]);
}

function runAuditArgs(args) {
  try {
    const stdout = execFileSync("node", [auditScript, ...args], {
      cwd: root,
      encoding: "utf8",
    });
    return { code: 0, stdout, stderr: "" };
  } catch (error) {
    return { code: error.status, stdout: error.stdout ?? "", stderr: error.stderr ?? "" };
  }
}

function writePolicy(dir, rules) {
  const policyPath = path.join(dir, "live-maya-policy.json");
  fs.writeFileSync(policyPath, `${JSON.stringify({ schema_version: 1, rules }, null, 2)}\n`);
  return policyPath;
}

function fixtureRepo(files) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "live-policy-audit-"));
  for (const [relative, content] of Object.entries(files)) {
    const filePath = path.join(dir, relative);
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, content);
  }
  execFileSync("git", ["init", "--quiet"], { cwd: dir });
  execFileSync("git", ["add", "--all"], { cwd: dir });
  return dir;
}
