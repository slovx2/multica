# Fork desktop updates

`fork-desktop-release.yml` packages published GitHub releases from
`slovx2/multica`. It overrides the upstream publish owner in `app-update.yml`.
macOS arm64 uses `latest-mac.yml`, macOS x64 uses `latest-x64-mac.yml`, and
Windows x64/arm64 use `latest.yml` / `latest-arm64.yml` respectively.

Fork tags are `vX.Y.Z.N`, while the packaged app and CLI use semver `X.Y.Z-N`.
The workflow sets `MULTICA_FORK_DESKTOP=true`, which electron-vite embeds in
the main process bundle. Fork builds disable electron-updater's prerelease
selection and downgrades. This makes the provider select the latest published
fork release and use its actual tag when resolving assets; otherwise the
numeric semver suffix is mistaken for a prerelease channel and no published
four-component tag matches. Keep this flag when packaging fork releases.
Upstream builds retain their existing release selection behavior.

The app enables automatic checks by default, five seconds after launch and
hourly thereafter. Users can toggle this or check manually in Settings →
Updates. Downloads run in the background; the restart notification appears
only after an update has downloaded successfully.

## Existing installations and signing

Versions `0.6.1-1` and `0.6.1-2` predate the fork channel fix. Install a build
containing the fix manually once; publishing a new release cannot change
the updater code already running on those installations.

Versions `0.6.1-1` and `0.6.1-2` were ad-hoc signed and not notarized. New
fork macOS builds require repository secrets `CSC_LINK`, `CSC_KEY_PASSWORD`,
`APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, and `APPLE_TEAM_ID`. The workflow
passes them only to macOS packaging, requires Developer ID signing and
notarization, and verifies the signature, stapled ticket, and Gatekeeper
assessment before uploading. Missing credentials fail the build instead of
silently producing an ad-hoc package. Windows packages remain unsigned.

Releases remain drafts until both platforms finish uploading their packages
and update metadata. Only then does the workflow publish the release as latest,
so installed clients do not discover an incomplete release.

A correct feed and a successful metadata check do not establish that native
macOS update installation works. Verify an actual signed-version-to-signed-version
update on a Mac before claiming end-to-end automatic updates. Keep the signing
identity consistent across releases. Never put private keys or passwords in
source, issues, or release notes.

References: [Electron autoUpdater](https://www.electronjs.org/docs/latest/api/auto-updater),
[Electron code signing](https://www.electronjs.org/docs/latest/tutorial/code-signing).

## Platform skill for agents outside Multica

Desktop builds include `skills/multica-platform-local` in the application's
resources directory. It is generated from the same commit's server-side
`multica-platform`: only the frontmatter name and description differ. The local
description tells agents to prefer the Multica-provided `multica-platform` when
that skill is already available in a task. The distinct name avoids a collision
with the skill delivered by the backend.

After installing a desktop build that includes this change, create a link for
each agent you use. For Claude Code on macOS:

```bash
mkdir -p ~/.claude/skills
ln -s /Applications/Multica.app/Contents/Resources/skills/multica-platform-local ~/.claude/skills/multica-platform-local
```

For current Codex versions, the user-level directory is `~/.agents/skills`, and
symlinked skill folders are supported ([official documentation](https://learn.chatgpt.com/docs/build-skills#where-codex-loads-local-skills)):

```bash
mkdir -p ~/.agents/skills
ln -s /Applications/Multica.app/Contents/Resources/skills/multica-platform-local ~/.agents/skills/multica-platform-local
```

Other agents use their own user-level skills directories. Adjust the source
path if Multica is installed somewhere other than `/Applications`. Keep the
application at the same path when upgrading: the link then points to the new
bundled contents automatically. Restart the agent if it retains old skill data.
If the destination already exists, inspect it before replacing it; the commands
above do not overwrite an existing skill.

On Windows, use a directory junction from Command Prompt. Replace the example
installation path with the directory containing the installed `Multica.exe`:

```bat
mkdir "%USERPROFILE%\.claude\skills"
mklink /J "%USERPROFILE%\.claude\skills\multica-platform-local" "C:\path\to\Multica\resources\skills\multica-platform-local"
```

For Codex, substitute `%USERPROFILE%\.agents\skills` for the destination parent.
Keep the installation path stable across updates. This does not install or
configure the CLI: the external agent still needs `multica` on PATH and its
normal authenticated workspace configuration.

### Build and verify

`pnpm dev:desktop`, the desktop `build` script, and `scripts/package.mjs`
regenerate `apps/desktop/resources/skills/multica-platform-local`. The generated
directory is ignored by Git. `fork-desktop-release.yml` already uses the package
script, so it also generates the copy. The `afterPack` hook checks the actual
macOS/Windows/Linux resources directory against the source file list and
contents, allowing only the two frontmatter edits; a missing or stale copy fails
packaging before release upload.

From the repository root, run the script tests and optionally check real local
packaging output with a minimal app fixture (no release or publishing):

```bash
pnpm --filter @multica/desktop exec vitest run scripts/bundle-platform-skill.test.mjs scripts/package.test.mjs
MULTICA_RUN_PLATFORM_SKILL_PACKAGE_SMOKE=1 pnpm --filter @multica/desktop exec vitest run scripts/bundle-platform-skill.test.mjs
```

The optional smoke check packages a Linux x64 directory using the production
`electron-builder.yml` and requires access to the matching Electron distribution
(downloaded or cached). It verifies the copied resource and the real `afterPack`
hook. Release installers still follow the normal reviewed-PR → next desktop tag
→ GitHub Actions sequence; users create their links after installing that build.
