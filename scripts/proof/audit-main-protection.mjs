import { execFileSync } from "node:child_process";
import fs from "node:fs";

const args = parseArgs(process.argv.slice(2));
const protection = args.input
  ? JSON.parse(fs.readFileSync(args.input, "utf8"))
  : JSON.parse(execFileSync("gh", ["api", `repos/${args.repository}/branches/main/protection`], { encoding: "utf8" }));
const findings = [];
const checks = protection.required_status_checks?.checks ?? [];
const appId = Number(args.app_id);

if (checks.length !== 1 || checks[0]?.context !== "CI / Required" || checks[0]?.app_id !== appId) {
  findings.push(`required status checks must contain only CI / Required pinned to app ${appId}`);
}
if (protection.enforce_admins?.enabled !== true) findings.push("administrator enforcement is disabled");
if (protection.allow_force_pushes?.enabled !== false) findings.push("force pushes are not explicitly disabled");
if (protection.allow_deletions?.enabled !== false) findings.push("branch deletion is not explicitly disabled");

if (findings.length > 0) {
  console.error("main protection audit failed");
  for (const finding of findings) console.error(`- ${finding}`);
  process.exit(1);
}
console.log("main protection audit passed: CI / Required pinned to GitHub Actions; admins enforced; force pushes and deletion disabled");

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i += 2) {
    const option = argv[i];
    const value = argv[i + 1];
    if (!["--input", "--repository", "--app-id"].includes(option) || !value) fail(`invalid option ${option ?? ""}`);
    parsed[option.slice(2).replaceAll("-", "_")] = value;
  }
  if (!parsed.input && !parsed.repository) fail("--repository or --input is required");
  if (!/^\d+$/.test(parsed.app_id ?? "")) fail("--app-id is required and must be numeric");
  return parsed;
}

function fail(message) {
  console.error(`audit-main-protection: ${message}`);
  process.exit(2);
}
