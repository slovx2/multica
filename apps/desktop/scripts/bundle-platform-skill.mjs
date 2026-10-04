#!/usr/bin/env node
// Generate the desktop copy without changing the server's embedded skill.
// The default export is electron-builder's afterPack check of the actual bundle.
import { cpSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const desktopRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const sourceSkillDir = resolve(
  desktopRoot,
  "../../server/internal/service/builtin_skills/multica-platform",
);
export const localSkillName = "multica-platform-local";
export const generatedSkillDir = join(desktopRoot, "resources/skills", localSkillName);
const descriptionPrefix =
  "Local copy bundled with Multica Desktop. If this task already includes " +
  "multica-platform provided by Multica, use that provided version. ";

export function localSkillContent(content) {
  const header = content.match(/^---\r?\n([\s\S]*?)\r?\n---(?=\r?\n|$)/);
  if (!header || !/^name: multica-platform\r?$/m.test(header[1])) {
    throw new Error("[bundle-platform-skill] expected multica-platform frontmatter");
  }
  // The built-in template stores its description as a quoted, single-line string.
  const description = header[1].match(/^description: ("[^\r\n]*")\r?$/m);
  if (!description) {
    throw new Error("[bundle-platform-skill] expected a quoted description");
  }
  const rewritten = header[0]
    .replace(/^name: multica-platform\r?$/m, `name: ${localSkillName}`)
    .replace(
      description[0],
      `description: ${JSON.stringify(descriptionPrefix + JSON.parse(description[1]))}`,
    );
  return rewritten + content.slice(header[0].length);
}

export function bundlePlatformSkill({
  sourceDir = sourceSkillDir,
  destinationDir = generatedSkillDir,
} = {}) {
  const content = localSkillContent(readFileSync(join(sourceDir, "SKILL.md"), "utf8"));
  rmSync(destinationDir, { recursive: true, force: true });
  cpSync(sourceDir, destinationDir, { recursive: true });
  writeFileSync(join(destinationDir, "SKILL.md"), content);
  return destinationDir;
}

function fileList(root, prefix = "") {
  return readdirSync(join(root, prefix), { withFileTypes: true })
    .flatMap((entry) => {
      const path = join(prefix, entry.name);
      return entry.isDirectory() ? fileList(root, path) : [path];
    })
    .sort();
}

export function verifyPlatformSkill(destinationDir, sourceDir = sourceSkillDir) {
  const expectedFiles = fileList(sourceDir);
  const actualFiles = fileList(destinationDir);
  if (JSON.stringify(actualFiles) !== JSON.stringify(expectedFiles)) {
    throw new Error("[bundle-platform-skill] packaged skill file list differs from source");
  }
  for (const file of expectedFiles) {
    const source = readFileSync(join(sourceDir, file));
    const expected = file === "SKILL.md" ? Buffer.from(localSkillContent(source.toString("utf8"))) : source;
    if (!readFileSync(join(destinationDir, file)).equals(expected)) {
      throw new Error(`[bundle-platform-skill] packaged skill content differs: ${file}`);
    }
  }
}

export default function verifyPackagedPlatformSkill(context) {
  const destinationDir = join(
    context.packager.getResourcesDir(context.appOutDir),
    "skills",
    localSkillName,
  );
  verifyPlatformSkill(destinationDir);
  console.log(`[bundle-platform-skill] verified packaged skill → ${destinationDir}`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  console.log(`[bundle-platform-skill] generated → ${bundlePlatformSkill()}`);
}
