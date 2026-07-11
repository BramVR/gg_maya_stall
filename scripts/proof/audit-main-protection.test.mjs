import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const script = path.join(root, "scripts", "proof", "audit-main-protection.mjs");

test("main protection audit accepts the single stable required result", () => {
  assert.doesNotThrow(() => run({
    required_status_checks: { checks: [{ context: "CI / Required", app_id: 98765 }] },
    enforce_admins: { enabled: true },
    allow_force_pushes: { enabled: false },
    allow_deletions: { enabled: false },
  }));
});

test("main protection audit rejects missing required result or bypassable main", () => {
  for (const patch of [
    { required_status_checks: { checks: [{ context: "old check", app_id: 98765 }] } },
    { required_status_checks: { contexts: ["CI / Required"] } },
    { required_status_checks: { checks: [{ context: "CI / Required", app_id: null }] } },
    { required_status_checks: { checks: [{ context: "CI / Required", app_id: 98765 }, { context: "Proof Manifest, Local Gates", app_id: 98765 }] } },
    { enforce_admins: { enabled: false } },
    { allow_force_pushes: { enabled: true } },
    { allow_deletions: { enabled: true } },
  ]) assert.throws(() => run({
    required_status_checks: { checks: [{ context: "CI / Required", app_id: 98765 }] },
    enforce_admins: { enabled: true },
    allow_force_pushes: { enabled: false },
    allow_deletions: { enabled: false },
    ...patch,
  }), /main protection audit failed/);
});

function run(value) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "maya-stall-protection-"));
  const input = path.join(dir, "protection.json");
  fs.writeFileSync(input, JSON.stringify(value));
  return execFileSync("node", [script, "--input", input, "--app-id", "98765"], { cwd: root, stdio: "pipe" });
}
