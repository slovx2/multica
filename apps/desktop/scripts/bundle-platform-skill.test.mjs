// @vitest-environment node
import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  bundlePlatformSkill,
  generatedSkillDir,
  sourceSkillDir,
  verifyPlatformSkill,
} from "./bundle-platform-skill.mjs";

const roots = [];
function tempRoot() {
  const root = mkdtempSync(join(tmpdir(), "multica-platform-skill-"));
  roots.push(root);
  return root;
}
afterEach(() => {
  while (roots.length) rmSync(roots.pop(), { recursive: true, force: true });
});

function files(root) {
  return readdirSync(root, { recursive: true })
    .filter((path) => statSync(join(root, path)).isFile())
    .sort();
}

describe("desktop platform skill", () => {
  it("renames only name/description and copies every supporting file unchanged", () => {
    const sourceBefore = new Map(files(sourceSkillDir).map((file) => [file, readFileSync(join(sourceSkillDir, file))]));
    const destinationDir = join(tempRoot(), "skills", "multica-platform-local");
    bundlePlatformSkill({ destinationDir });
    expect(files(destinationDir)).toEqual([...sourceBefore.keys()]);
    const source = sourceBefore.get("SKILL.md").toString("utf8");
    const local = readFileSync(join(destinationDir, "SKILL.md"), "utf8");
    const frontmatter = local.split("---")[1];
    expect(frontmatter).toMatch(/^name: multica-platform-local$/m);
    const description = JSON.parse(frontmatter.match(/^description: (.+)$/m)[1]);
    expect(description).toMatch(/^Local copy bundled with Multica Desktop\./);
    expect(description).toContain("If this task already includes multica-platform provided by Multica, use that provided version.");
    const sourceDescription = JSON.parse(source.match(/^description: (.+)$/m)[1]);
    expect(description.endsWith(sourceDescription)).toBe(true);
    // All other frontmatter and the complete instruction body must survive byte-for-byte.
    const withoutEditedFields = (text) => text.replace(/^(name|description): .*\r?\n/gm, "");
    expect(withoutEditedFields(local)).toBe(withoutEditedFields(source));
    for (const [file, before] of sourceBefore) {
      expect(readFileSync(join(sourceSkillDir, file))).toEqual(before);
      if (file !== "SKILL.md") expect(readFileSync(join(destinationDir, file))).toEqual(before);
    }
    expect(() => verifyPlatformSkill(destinationDir)).not.toThrow();
  });

  it("refreshes changed references and removes files deleted from the source", () => {
    const root = tempRoot();
    const sourceDir = join(root, "source");
    const destinationDir = join(root, "generated");
    cpSync(sourceSkillDir, sourceDir, { recursive: true });
    bundlePlatformSkill({ sourceDir, destinationDir });
    const reference = join("references", "projects.md");
    writeFileSync(join(sourceDir, reference), "updated resource documentation\n");
    rmSync(join(sourceDir, "references", "agents.md"));
    bundlePlatformSkill({ sourceDir, destinationDir });
    expect(readFileSync(join(destinationDir, reference), "utf8")).toBe("updated resource documentation\n");
    expect(existsSync(join(destinationDir, "references", "agents.md"))).toBe(false);
    expect(() => verifyPlatformSkill(destinationDir, sourceDir)).not.toThrow();
  });

  it("fails on missing or malformed source rather than shipping a stale copy", () => {
    const root = tempRoot();
    const destinationDir = join(root, "generated");
    expect(() => bundlePlatformSkill({ sourceDir: join(root, "missing"), destinationDir })).toThrow();
    const sourceDir = join(root, "malformed");
    mkdirSync(sourceDir);
    writeFileSync(join(sourceDir, "SKILL.md"), "---\nname: other-skill\n---\n");
    expect(() => bundlePlatformSkill({ sourceDir, destinationDir })).toThrow(/expected multica-platform frontmatter/);
    writeFileSync(join(sourceDir, "SKILL.md"), "---\nname: multica-platform\n---\n");
    expect(() => bundlePlatformSkill({ sourceDir, destinationDir })).toThrow(/expected a quoted description/);
  });

  it.each(["missing", "extra", "changed", "wrong-name"])("rejects a packaged copy with %s content", (kind) => {
    const destinationDir = join(tempRoot(), "generated");
    bundlePlatformSkill({ destinationDir });
    if (kind === "missing") rmSync(join(destinationDir, "references", "projects.md"));
    if (kind === "extra") writeFileSync(join(destinationDir, "stale.md"), "stale");
    if (kind === "changed") writeFileSync(join(destinationDir, "references", "projects.md"), "different");
    if (kind === "wrong-name") cpSync(join(sourceSkillDir, "SKILL.md"), join(destinationDir, "SKILL.md"));
    expect(() => verifyPlatformSkill(destinationDir)).toThrow(/packaged skill/);
  });

  // Opt in locally to exercise the real packager without publishing or building
  // Multica's renderer/CLI. The normal release invokes the same afterPack check.
  it.skipIf(process.env.MULTICA_RUN_PLATFORM_SKILL_PACKAGE_SMOKE !== "1")(
    "includes the skill in real electron-builder output using the production config",
    () => {
      const root = tempRoot();
      const appDir = join(root, "app");
      const output = join(root, "output");
      mkdirSync(appDir);
      writeFileSync(join(appDir, "index.js"), "// Packaging fixture; never executed.\n");
      writeFileSync(join(appDir, "package.json"), JSON.stringify({ name: "platform-skill-smoke", version: "1.0.0", main: "index.js", description: "Packaging fixture", author: "Multica" }));
      const desktopRoot = resolve(generatedSkillDir, "../../..");
      bundlePlatformSkill();
      const options = {
        projectDir: desktopRoot,
        publish: "never",
        config: {
          extends: join(desktopRoot, "electron-builder.yml"),
          directories: { app: appDir, output },
          files: ["package.json", "index.js"],
          extraMetadata: { main: "index.js" },
        },
      };
      execFileSync(process.execPath, ["--input-type=module", "-e", `
        import { build, Platform, Arch } from "electron-builder";
        await build({ ...JSON.parse(process.argv[1]), targets: Platform.LINUX.createTarget("dir", Arch.x64) });
      `, JSON.stringify(options)], {
        cwd: desktopRoot,
        env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: "false" },
        stdio: "inherit",
        timeout: 120_000,
      });
      const packagedSkill = join(output, "linux-unpacked", "resources", "skills", "multica-platform-local");
      expect(existsSync(join(packagedSkill, "SKILL.md"))).toBe(true);
      verifyPlatformSkill(packagedSkill);
    },
    130_000,
  );
});
