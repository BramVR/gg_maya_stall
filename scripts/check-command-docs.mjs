import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";

const root = path.resolve(new URL("..", import.meta.url).pathname);
const docsDir = path.join(root, "docs", "commands");
const indexPath = path.join(docsDir, "README.md");

const help = execFileSync("go", ["run", "./cmd/maya-stall", "--help"], {
  cwd: root,
  encoding: "utf8",
});

const commands = parseCommands(help);
const expectedDocs = new Map();

for (const command of commands) {
  expectedDocs.set(commandDoc(command), command);
}

const missing = [];
const index = fs.readFileSync(indexPath, "utf8");

for (const [doc, command] of expectedDocs) {
  const docPath = path.join(docsDir, doc);
  if (!fs.existsSync(docPath)) {
    missing.push(`${command}: docs/commands/${doc} missing`);
    continue;
  }
  if (!index.includes(`](${doc})`)) {
    missing.push(`${command}: docs/commands/README.md does not link ${doc}`);
  }
}

if (missing.length > 0) {
  console.error(missing.join("\n"));
  process.exit(1);
}

console.log(`checked ${commands.length} CLI commands: command docs ok`);

function parseCommands(helpText) {
  const lines = helpText.split(/\r?\n/);
  const start = lines.findIndex((line) => line.trim() === "Commands:");
  if (start === -1) {
    throw new Error("CLI help does not contain a Commands section");
  }

  const commands = [];
  for (const line of lines.slice(start + 1)) {
    if (line.trim() === "") {
      break;
    }
    const match = line.match(/^\s{2}(.+?)\s{2,}/);
    if (match) {
      commands.push(match[1].trim());
    }
  }

  if (commands.length === 0) {
    throw new Error("CLI help Commands section was empty");
  }
  return commands;
}

function commandDoc(command) {
  const firstWord = command.split(/\s+/)[0];
  return `${firstWord}.md`;
}
