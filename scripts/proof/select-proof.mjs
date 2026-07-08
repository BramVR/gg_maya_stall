import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const defaultPolicyPath = path.join(root, "proof", "live-maya-policy.json");
const args = parseArgs(process.argv.slice(2));
const policyPath = path.resolve(root, args.policy ?? defaultPolicyPath);
const outputPath = path.resolve(root, args.output ?? path.join("artifacts", "proof", "proof-manifest.json"));
const policy = readJSON(policyPath);
const changedFiles = uniqueChangedFiles(readChangedFiles(args));
const liveReasons = selectLiveReasons(policy, changedFiles);
const liveRequired = liveReasons.length > 0;

const manifest = {
  schema_version: 1,
  generated_at: new Date().toISOString(),
  policy: path.relative(root, policyPath).replaceAll(path.sep, "/"),
  base: args.base ?? "",
  head: args.head ?? "",
  changed_files: changedFiles,
  live_maya_required: liveRequired,
  live_maya_reasons: liveReasons,
  gates: {
    local: { status: "pending", command: "go test ./..." },
    docs: { status: "pending", command: "scripts/check-docs.sh" },
    artifacts: { status: "pending", required: true },
    live_maya: {
      status: liveRequired ? "required" : "not_required",
      command: "go test ./internal/cli -run TestOptInRealVisualEvidenceSmoke -count=1 && go test ./internal/cli -run TestOptInRealDesktopControlModalSmoke -count=1 && go test ./internal/cli -run TestOptInRealRunScopedDesktopOpsSmoke -count=1 && go test ./internal/cli -run 'TestOptInRealSSH(Doctor|Run|ConsumingRepo)Smoke' -count=1",
      fail_closed: liveRequired,
    },
  },
};

fs.mkdirSync(path.dirname(outputPath), { recursive: true });
fs.writeFileSync(outputPath, `${JSON.stringify(manifest, null, 2)}\n`);
writeGitHubOutput({
  manifest: path.relative(root, outputPath).replaceAll(path.sep, "/"),
  live_maya_required: liveRequired ? "true" : "false",
});
console.log(`proof_manifest: ${path.relative(root, outputPath).replaceAll(path.sep, "/")}`);
console.log(`live_maya_required: ${liveRequired ? "true" : "false"}`);
if (liveReasons.length > 0) {
  for (const reason of liveReasons) {
    console.log(`live_maya_reason: ${reason.rule} ${reason.path}`);
  }
}

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case "--policy":
      case "--output":
      case "--changed-files":
      case "--base":
      case "--head":
        i++;
        if (i >= argv.length || argv[i] === "") {
          fail(`${arg} needs a value`);
        }
        parsed[arg.slice(2).replaceAll("-", "_")] = argv[i];
        break;
      default:
        fail(`unknown option ${arg}`);
    }
  }
  parsed.changedFiles = parsed.changed_files;
  parsed.base = parsed.base ?? process.env.MAYA_STALL_PROOF_BASE ?? "";
  parsed.head = parsed.head ?? process.env.MAYA_STALL_PROOF_HEAD ?? "HEAD";
  return parsed;
}

function readChangedFiles(options) {
  if (options.changedFiles) {
    return fs.readFileSync(path.resolve(root, options.changedFiles), "utf8").split(/\r?\n/);
  }
  if (!options.base) {
    return execFileSync("git", ["diff", "--name-status", "--diff-filter=ACDMRT", "HEAD"], {
      cwd: root,
      encoding: "utf8",
    }).split(/\r?\n/);
  }
  return execFileSync("git", ["diff", "--name-status", "--diff-filter=ACDMRT", `${options.base}...${options.head}`], {
    cwd: root,
    encoding: "utf8",
  }).split(/\r?\n/);
}

function uniqueChangedFiles(lines) {
  const files = [];
  const seen = new Set();
  for (const line of lines) {
    for (const file of changedFilesFromLine(line)) {
      if (!seen.has(file)) {
        seen.add(file);
        files.push(file);
      }
    }
  }
  return files;
}

function changedFilesFromLine(line) {
  const trimmed = line.trim();
  if (!trimmed) {
    return [];
  }
  const fields = trimmed.split(/\t+/);
  if (fields.length === 1) {
    return [normalizePath(fields[0])].filter(Boolean);
  }
  const status = fields[0];
  if (/^[RC]\d*/.test(status)) {
    return fields.slice(1, 3).map(normalizePath).filter(Boolean);
  }
  return [normalizePath(fields[1])].filter(Boolean);
}

function selectLiveReasons(policy, changedFiles) {
  const reasons = [];
  for (const file of changedFiles) {
    for (const rule of policy.rules ?? []) {
      if (matchesRule(file, rule)) {
        reasons.push({ path: file, rule: rule.id, reason: rule.reason });
        break;
      }
    }
  }
  return reasons;
}

function matchesRule(file, rule) {
  return (rule.paths ?? []).includes(file) ||
    (rule.prefixes ?? []).some((prefix) => file.startsWith(prefix));
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function normalizePath(file) {
  return file.trim().replaceAll("\\", "/").replace(/^\.\/+/, "");
}

function writeGitHubOutput(values) {
  const githubOutput = process.env.GITHUB_OUTPUT;
  if (!githubOutput) {
    return;
  }
  const lines = Object.entries(values).map(([key, value]) => `${key}=${value}`);
  fs.appendFileSync(githubOutput, `${lines.join("\n")}\n`);
}

function fail(message) {
  console.error(`select-proof: ${message}`);
  process.exit(2);
}
