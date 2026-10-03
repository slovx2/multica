// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import type { AppUpdater } from "electron-updater";
import { GitHubProvider } from "electron-updater/out/providers/GitHubProvider";
import type { ProviderRuntimeOptions } from "electron-updater/out/providers/Provider";
import { configureForkUpdateChannel } from "./fork-update-channel";

function fixture(channel: string | null, platform: "darwin" | "win32") {
  const updater = {
    currentVersion: "0.6.1-1",
    allowPrerelease: true,
    allowDowngrade: true,
    channel,
  };
  const requests: string[] = [];
  const executor = {
    request: vi.fn(async (options: { hostname: string; path: string }) => {
      expect(options.hostname).toBe("github.com");
      requests.push(options.path);
      if (options.path === "/slovx2/multica/releases.atom") {
        return '<feed><entry><title>v0.6.1.2</title><link href="https://github.com/slovx2/multica/releases/tag/v0.6.1.2"/><content>Fork update</content></entry></feed>';
      }
      if (options.path === "/slovx2/multica/releases/latest") {
        return JSON.stringify({ tag_name: "v0.6.1.2" });
      }
      if (options.path.startsWith("/slovx2/multica/releases/download/v0.6.1.2/")) {
        return "version: 0.6.1-2\nfiles:\n  - url: desktop.zip\n    sha512: checksum\n";
      }
      throw new Error(`Unexpected URL: ${options.path}`);
    }),
  };
  const provider = new GitHubProvider(
    { provider: "github", owner: "slovx2", repo: "multica" },
    updater as unknown as AppUpdater,
    { platform, isUseMultipleRangeRequest: false, executor } as unknown as ProviderRuntimeOptions,
  );
  return { updater, provider, requests };
}

describe("fork published release updates", () => {
  it("reproduces the numeric prerelease channel failure in the real provider", async () => {
    const { provider } = fixture(null, "darwin");
    await expect(provider.getLatestVersion()).rejects.toMatchObject({
      code: "ERR_UPDATER_NO_PUBLISHED_VERSIONS",
    });
  });

  it.each([
    ["darwin", null, "latest-mac.yml"],
    ["darwin", "latest-x64", "latest-x64-mac.yml"],
    ["win32", null, "latest.yml"],
    ["win32", "latest-arm64", "latest-arm64.yml"],
  ] as const)("resolves %s / %s from the fork feed", async (platform, channel, file) => {
    const { updater, provider, requests } = fixture(channel, platform);
    configureForkUpdateChannel(updater, true);
    const info = await provider.getLatestVersion();
    expect(info.version).toBe("0.6.1-2");
    expect(requests.at(-1)).toBe(`/slovx2/multica/releases/download/v0.6.1.2/${file}`);
    expect(provider.resolveFiles(info)[0].url.href).toBe(
      "https://github.com/slovx2/multica/releases/download/v0.6.1.2/desktop.zip",
    );
    expect(updater.allowDowngrade).toBe(false);
  });

  it("preserves upstream release behavior", () => {
    const updater = { allowPrerelease: true, allowDowngrade: true };
    configureForkUpdateChannel(updater, false);
    expect(updater).toEqual({ allowPrerelease: true, allowDowngrade: true });
  });
});
