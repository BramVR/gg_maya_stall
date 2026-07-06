import fs from "node:fs";
import path from "node:path";

const root = path.resolve(new URL("../..", import.meta.url).pathname);
const args = parseArgs(process.argv.slice(2));
const paths = args.paths.length > 0 ? args.paths : [path.join("artifacts", "proof")];
const findings = [];
const mediaFiles = [];

const forbiddenPatterns = [
  { label: "private key", regex: /BEGIN (?:OPENSSH|RSA|DSA|EC|PRIVATE)(?: PRIVATE)? KEY/i },
  { label: "ssh public key", regex: /ssh-(?:rsa|ed25519)\s+[A-Za-z0-9+/=]+/i },
  { label: "token variable", regex: /\b(?:GITHUB_TOKEN|GH_TOKEN|GITLAB_TOKEN)\b/i },
  { label: "license variable", regex: /\b(?:MAYA_LICENSE|ADSKFLEX|LM_LICENSE_FILE)\b/i },
  { label: "secret assignment", regex: /"?\b(?:password|token|secret)"?\s*[:=]/i },
  { label: "ssh material path", regex: /\.ssh\//i },
  { label: "mac user path", regex: /\/Users\/[^/\s]+/ },
  { label: "windows user path", regex: /C:\\Users\\[^\\\s]+/i },
  { label: "private desktop host alias", regex: /"(?:selectedHostAlias|hostAlias|host)"\s*:\s*"desktop-[a-z0-9-]+"/i },
  { label: "windows machine user", regex: /\b[A-Z0-9-]+\\[A-Z0-9._-]+\b/i },
];

for (const inputPath of paths) {
  const absolute = path.resolve(root, inputPath);
  if (!fs.existsSync(absolute)) {
    findings.push(`${inputPath}: missing`);
    continue;
  }
  for (const file of walkFiles(absolute)) {
    const relative = path.relative(root, file).replaceAll(path.sep, "/");
    if (fs.lstatSync(file).isSymbolicLink()) {
      findings.push(`${relative}: symlink not allowed`);
      continue;
    }
    if (isMediaFile(file)) {
      mediaFiles.push({ absolute: file, relative });
    }
    if (!isScannedTextFile(file)) continue;
    const content = fs.readFileSync(file, "utf8");
    for (const pattern of forbiddenPatterns) {
      const match = content.match(pattern.regex);
      if (match) {
        findings.push(`${relative}: ${pattern.label}`);
      }
    }
  }
}

requireMediaReview(mediaFiles);

if (findings.length > 0) {
  console.error("public artifact confidentiality gate failed");
  for (const finding of findings) console.error(`- ${finding}`);
  process.exit(1);
}

console.log("public artifact confidentiality gate passed");

function parseArgs(argv) {
  const parsed = { paths: [] };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    switch (arg) {
      case "--path":
        i++;
        if (i >= argv.length || argv[i] === "") fail("--path needs a value");
        parsed.paths.push(argv[i]);
        break;
      default:
        fail(`unknown option ${arg}`);
    }
  }
  return parsed;
}

function* walkFiles(inputPath) {
  const info = fs.lstatSync(inputPath);
  if (info.isSymbolicLink()) {
    yield inputPath;
    return;
  }
  if (info.isFile()) {
    yield inputPath;
    return;
  }
  if (!info.isDirectory()) return;
  for (const entry of fs.readdirSync(inputPath).sort()) {
    yield* walkFiles(path.join(inputPath, entry));
  }
}

function isScannedTextFile(file) {
  const extension = path.extname(file).toLowerCase();
  return [".json", ".jsonl", ".md", ".txt", ".log", ".yml", ".yaml", ".sh", ".ps1", ".env"].includes(extension);
}

function isMediaFile(file) {
  return [".png", ".mp4"].includes(path.extname(file).toLowerCase());
}

function requireMediaReview(files) {
  if (files.length === 0) return;
  const byRoot = new Map();
  for (const file of files) {
    const root = artifactRootFor(file.absolute);
    const entries = byRoot.get(root) ?? [];
    entries.push(file);
    byRoot.set(root, entries);
  }
  for (const [rootDir, entries] of byRoot.entries()) {
    const reviewPath = path.join(rootDir, "media-review.json");
    if (!fs.existsSync(reviewPath)) {
      findings.push(`${path.relative(root, rootDir).replaceAll(path.sep, "/")}: media-review.json missing for uploaded desktop media`);
      continue;
    }
    const review = JSON.parse(fs.readFileSync(reviewPath, "utf8"));
    const reviewedPaths = new Set((review.paths ?? []).map((value) => String(value).replaceAll("\\", "/")));
    if (review.reviewed !== true) {
      findings.push(`${path.relative(root, reviewPath).replaceAll(path.sep, "/")}: reviewed must be true`);
    }
    for (const entry of entries) {
      const relativeToArtifact = path.relative(rootDir, entry.absolute).replaceAll(path.sep, "/");
      if (!reviewedPaths.has(relativeToArtifact)) {
        findings.push(`${entry.relative}: media path missing from media-review.json`);
      }
    }
  }
}

function artifactRootFor(file) {
  let current = path.dirname(file);
  while (current !== path.dirname(current)) {
    if (fs.existsSync(path.join(current, "proof-artifact-manifest.json")) || fs.existsSync(path.join(current, "media-review.json"))) {
      return current;
    }
    current = path.dirname(current);
  }
  return path.dirname(file);
}

function fail(message) {
  console.error(`assert-public-artifact-confidentiality: ${message}`);
  process.exit(2);
}
