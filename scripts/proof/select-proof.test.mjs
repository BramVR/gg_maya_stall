import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import test from "node:test";
import assert from "node:assert/strict";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const selectScript = path.join(root, "scripts", "proof", "select-proof.mjs");
const assertScript = path.join(root, "scripts", "proof", "assert-live-proof.mjs");

test("selector requires live Maya proof for live product behavior paths", () => {
  const dir = tempDir();
  const changed = path.join(dir, "changed.txt");
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(changed, "internal/cli/sessiond_broker.go\nREADME.md\n");

  execFileSync("node", [
    selectScript,
    "--changed-files",
    changed,
    "--output",
    manifestPath,
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.live_maya_required, true);
  assert.deepEqual(manifest.changed_files, [
    "internal/cli/sessiond_broker.go",
    "README.md",
  ]);
  assert.equal(manifest.live_maya_reasons[0].rule, "session-broker");
  assert.equal(manifest.gates.live_maya.status, "required");
  assert.equal(manifest.gates.live_maya.command, "go test ./internal/cli -run TestOptInRealVisualEvidenceSmoke -count=1 && go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run|ConsumingRepo)Smoke' -count=1");
  assert.equal(manifest.gates.local.status, "pending");
  assert.equal(manifest.gates.docs.status, "pending");
  assert.equal(manifest.gates.artifacts.status, "pending");
});

test("selector allows docs-only changes with manifest saying live is not required", () => {
  const dir = tempDir();
  const changed = path.join(dir, "changed.txt");
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(changed, "docs/commands/version.md\n");

  execFileSync("node", [
    selectScript,
    "--changed-files",
    changed,
    "--output",
    manifestPath,
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.live_maya_required, false);
  assert.deepEqual(manifest.live_maya_reasons, []);
  assert.equal(manifest.gates.live_maya.status, "not_required");
});

test("selector requires live Maya proof for deleted watched paths", () => {
  const dir = tempDir();
  const changed = path.join(dir, "changed.txt");
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(changed, "D\tinternal/cli/sessiond_broker.go\n");

  execFileSync("node", [
    selectScript,
    "--changed-files",
    changed,
    "--output",
    manifestPath,
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.live_maya_required, true);
  assert.equal(manifest.changed_files[0], "internal/cli/sessiond_broker.go");
  assert.equal(manifest.live_maya_reasons[0].rule, "session-broker");
});

test("selector checks both old and new rename paths", () => {
  const dir = tempDir();
  const changed = path.join(dir, "changed.txt");
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(changed, "R100\tinternal/cli/sessiond_broker.go\tinternal/cli/broker_renamed.go\n");

  execFileSync("node", [
    selectScript,
    "--changed-files",
    changed,
    "--output",
    manifestPath,
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.live_maya_required, true);
  assert.deepEqual(manifest.changed_files, [
    "internal/cli/sessiond_broker.go",
    "internal/cli/broker_renamed.go",
  ]);
  assert.equal(manifest.live_maya_reasons[0].path, "internal/cli/sessiond_broker.go");
});

test("selector requires live Maya proof for new files under live source prefixes", () => {
  const dir = tempDir();
  const changed = path.join(dir, "changed.txt");
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(changed, "A\tinternal/cli/new_live_surface.go\n");

  execFileSync("node", [
    selectScript,
    "--changed-files",
    changed,
    "--output",
    manifestPath,
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.live_maya_required, true);
  assert.equal(manifest.live_maya_reasons[0].rule, "live-product-source");
});

test("assert-live-proof fails closed when required live proof has no host config", () => {
  const dir = tempDir();
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(manifestPath, JSON.stringify({
    schema_version: 1,
    live_maya_required: true,
    gates: {
      live_maya: { status: "required" },
    },
  }));

  assert.throws(() => {
    execFileSync("node", [
      assertScript,
      "--manifest",
      manifestPath,
      "--live-status",
      "skipped",
      "--host-config-state",
      "missing",
    ], { cwd: root, stdio: "pipe" });
  }, /live Maya proof is required/);
});

test("assert-live-proof accepts non-live changes without host config", () => {
  const dir = tempDir();
  const manifestPath = path.join(dir, "proof-manifest.json");
  fs.writeFileSync(manifestPath, JSON.stringify({
    schema_version: 1,
    live_maya_required: false,
    gates: {
      live_maya: { status: "not_required" },
    },
  }));

  execFileSync("node", [
    assertScript,
    "--manifest",
    manifestPath,
    "--live-status",
    "skipped",
    "--host-config-state",
    "missing",
  ], { cwd: root });

  const manifest = readJSON(manifestPath);
  assert.equal(manifest.gates.live_maya.status, "not_required");
});

function tempDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "maya-stall-proof-"));
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}
