import fs from "node:fs";
import path from "node:path";

const root = path.resolve(new URL("..", import.meta.url).pathname);
const docsDir = path.join(root, "docs");
const files = [
  path.join(root, "README.md"),
  ...walk(docsDir).filter((file) => file.endsWith(".md")),
];

const anchorCache = new Map();
const errors = [];

for (const file of files) {
  const text = fs.readFileSync(file, "utf8");
  for (const match of markdownLinks(text)) {
    const target = stripAngleBrackets(match.target.trim());
    if (skipTarget(target)) {
      continue;
    }

    const { pathname, hash } = splitMarkdownTarget(target);
    const baseDir = path.dirname(file);
    const targetPath = pathname === ""
      ? file
      : path.resolve(baseDir, decodeURIComponent(pathname));

    if (!targetPath.startsWith(root)) {
      errors.push(`${rel(file)}: link escapes repo: ${target}`);
      continue;
    }

    if (!fs.existsSync(targetPath)) {
      errors.push(`${rel(file)}: missing link target ${target}`);
      continue;
    }

    if (hash && fs.statSync(targetPath).isFile() && targetPath.endsWith(".md")) {
      const anchors = anchorsFor(targetPath);
      const anchor = decodeURIComponent(hash.slice(1));
      if (!anchors.has(anchor)) {
        errors.push(`${rel(file)}: missing anchor ${hash} in ${rel(targetPath)}`);
      }
    }
  }
}

if (errors.length > 0) {
  console.error(errors.join("\n"));
  process.exit(1);
}

console.log(`checked ${files.length} markdown files: internal links ok`);

function walk(dir) {
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...walk(fullPath));
    } else {
      files.push(fullPath);
    }
  }
  return files.sort();
}

function markdownLinks(text) {
  const links = [];
  const pattern = /!?\[[^\]]*\]\(([^)]+)\)/g;
  for (const match of text.matchAll(pattern)) {
    links.push({ target: match[1] });
  }
  return links;
}

function skipTarget(target) {
  return (
    target === "" ||
    target.startsWith("http://") ||
    target.startsWith("https://") ||
    target.startsWith("mailto:")
  );
}

function splitMarkdownTarget(target) {
  const hashIndex = target.indexOf("#");
  if (hashIndex === -1) {
    return { pathname: target, hash: "" };
  }
  return {
    pathname: target.slice(0, hashIndex),
    hash: target.slice(hashIndex),
  };
}

function anchorsFor(file) {
  if (anchorCache.has(file)) {
    return anchorCache.get(file);
  }
  const anchors = new Set();
  const text = fs.readFileSync(file, "utf8");
  for (const line of text.split(/\r?\n/)) {
    const match = line.match(/^(#{1,6})\s+(.+)$/);
    if (match) {
      anchors.add(slugify(match[2]));
    }
  }
  anchorCache.set(file, anchors);
  return anchors;
}

function slugify(text) {
  return trimDashes(
    text
      .toLowerCase()
      .replace(/`([^`]+)`/g, "$1")
      .replace(/<[^>]+>/g, "")
      .replace(/[^\p{Letter}\p{Number}\s-]/gu, "")
      .replace(/\s+/g, "-")
      .replace(/-+/g, "-"),
  );
}

function stripAngleBrackets(target) {
  if (target.startsWith("<") && target.endsWith(">")) {
    return target.slice(1, -1);
  }
  return target;
}

function trimDashes(value) {
  return value.replace(/^-+|-+$/g, "");
}

function rel(file) {
  return path.relative(root, file);
}
