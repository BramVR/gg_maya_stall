import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

// Audits proof/live-maya-policy.json against the working tree so policy
// coverage cannot silently drift away from the live-sensitive surface:
// - every rule `paths` entry must be a tracked file (catches renames and
//   deletions that would silently stop matching);
// - every rule `prefixes` entry must match at least one tracked file;
// - every tracked file under the live-sensitive roots must be matched by
//   at least one rule, so new files land under an existing rule instead of
//   escaping the live Maya gate.

const liveSensitiveRoots = [
  "cmd/",
  "internal/",
  "scripts/proof/",
  "scripts/windows/",
];

const args = parseArgs(process.argv.slice(2));
const root = path.resolve(args.root ?? fileURLToPath(new URL("../..", import.meta.url)));
const policyPath = path.resolve(root, args.policy ?? path.join("proof", "live-maya-policy.json"));

const violations = auditLivePolicy(root, policyPath);
if (violations.length > 0) {
  for (const violation of violations) {
    console.error(`live-policy-audit: ${violation}`);
  }
  process.exit(1);
}
console.log(`live-policy-audit: ok (${path.relative(root, policyPath)})`);

function auditLivePolicy(repoRoot, auditedPolicyPath) {
  let policy;
  try {
    policy = JSON.parse(fs.readFileSync(auditedPolicyPath, "utf8"));
  } catch (error) {
    return [`policy ${auditedPolicyPath} is unreadable: ${error.message}`];
  }
  const violations = [];
  if (policy.schema_version !== 1) {
    violations.push(`policy schema_version ${policy.schema_version} is not 1`);
  }
  const rules = Array.isArray(policy.rules) ? policy.rules : [];
  if (rules.length === 0) {
    violations.push("policy has no rules");
  }

  const trackedFiles = listTrackedFiles(repoRoot);
  const trackedSet = new Set(trackedFiles);

  for (const rule of rules) {
    const id = rule.id ?? "<missing id>";
    for (const rulePath of rule.paths ?? []) {
      if (!trackedSet.has(rulePath)) {
        violations.push(`rule ${id}: path ${rulePath} is not a tracked file (renamed or deleted?)`);
      }
    }
    for (const prefix of rule.prefixes ?? []) {
      if (!trackedFiles.some((file) => file.startsWith(prefix))) {
        violations.push(`rule ${id}: prefix ${prefix} matches no tracked files`);
      }
    }
  }

  for (const file of trackedFiles) {
    if (!liveSensitiveRoots.some((liveRoot) => file.startsWith(liveRoot))) {
      continue;
    }
    if (!matchesAnyRule(rules, file)) {
      violations.push(`live-sensitive file ${file} is not covered by any policy rule`);
    }
  }

  return violations;
}

function matchesAnyRule(rules, file) {
  return rules.some((rule) => {
    if ((rule.paths ?? []).includes(file)) {
      return true;
    }
    return (rule.prefixes ?? []).some((prefix) => file.startsWith(prefix));
  });
}

function listTrackedFiles(repoRoot) {
  const output = execFileSync("git", ["ls-files", "-z"], { cwd: repoRoot, encoding: "utf8" });
  return output.split("\0").filter(Boolean);
}

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i += 1) {
    const flag = argv[i];
    if (flag === "--policy" || flag === "--root") {
      i += 1;
      if (i >= argv.length || argv[i] === "") {
        fail(`${flag} needs a value`);
      }
      parsed[flag.slice(2)] = argv[i];
      continue;
    }
    fail(`unknown option ${flag}`);
  }
  return parsed;
}

function fail(message) {
  console.error(`live-policy-audit: ${message}`);
  process.exit(2);
}
