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

Current fork macOS packages use ad-hoc signing and are not notarized. A
correct feed and a successful metadata check do not establish that native
macOS update installation works. Electron's Squirrel.Mac updater requires a
consistent code-signing identity. For supported distribution, configure a
Developer ID Application certificate and notarization credentials through
GitHub Actions secrets, replace the workflow's forced ad-hoc identity, and
verify an actual signed-version-to-signed-version update on a Mac before
claiming end-to-end automatic updates. Never put private keys or passwords
in source, issues, or release notes.

References: [Electron autoUpdater](https://www.electronjs.org/docs/latest/api/auto-updater),
[Electron code signing](https://www.electronjs.org/docs/latest/tutorial/code-signing).
