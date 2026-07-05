import fs from "node:fs";
import path from "node:path";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const args = parseArgs(process.argv.slice(2));
const manifestPath = path.resolve(root, args.manifest ?? path.join("artifacts", "proof", "proof-manifest.json"));
const manifest = readJSON(manifestPath);
const liveStatus = args.live_status ?? "skipped";
const hostConfigState = args.host_config_state ?? "missing";

if (manifest.live_maya_required) {
  const missingConfig = hostConfigState !== "present";
  const missingProof = liveStatus !== "passed";
  if (missingConfig || missingProof) {
    manifest.gates = manifest.gates ?? {};
    manifest.gates.live_maya = {
      ...(manifest.gates.live_maya ?? {}),
      status: missingConfig ? "failed_missing_host_config" : `failed_${liveStatus}`,
      host_config_state: hostConfigState,
      fail_closed: true,
    };
    writeJSON(manifestPath, manifest);
    fail("live Maya proof is required for this change; skipped, missing, or fake-only proof fails closed");
  }
  manifest.gates.live_maya = {
    ...(manifest.gates.live_maya ?? {}),
    status: "passed",
    host_config_state: hostConfigState,
    fail_closed: true,
  };
} else {
  manifest.gates = manifest.gates ?? {};
  manifest.gates.live_maya = {
    ...(manifest.gates.live_maya ?? {}),
    status: "not_required",
    host_config_state: hostConfigState,
    fail_closed: false,
  };
}

writeJSON(manifestPath, manifest);
console.log(`live_maya_status: ${manifest.gates.live_maya.status}`);

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case "--manifest":
      case "--live-status":
      case "--host-config-state":
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
  return parsed;
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJSON(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

function fail(message) {
  console.error(`assert-live-proof: ${message}`);
  process.exit(1);
}
