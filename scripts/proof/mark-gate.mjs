import fs from "node:fs";
import path from "node:path";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const args = parseArgs(process.argv.slice(2));
const manifestPath = path.resolve(root, args.manifest ?? path.join("artifacts", "proof", "proof-manifest.json"));
const gate = args.gate;
const status = args.status;

if (!gate) {
  fail("--gate needs a value");
}
if (!status) {
  fail("--status needs a value");
}

const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
manifest.gates = manifest.gates ?? {};
manifest.gates[gate] = {
  ...(manifest.gates[gate] ?? {}),
  status,
};
if (args.command) {
  manifest.gates[gate].command = args.command;
}
if (args.detail) {
  manifest.gates[gate].detail = args.detail;
}
fs.writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`${gate}: ${status}`);

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case "--manifest":
      case "--gate":
      case "--status":
      case "--command":
      case "--detail":
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

function fail(message) {
  console.error(`mark-gate: ${message}`);
  process.exit(2);
}
