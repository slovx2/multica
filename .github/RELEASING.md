# Release runbook

## Normal release

Release from a reviewed commit on `main` by creating and pushing a new semantic
version tag such as `v0.18.4`. The Release workflow intentionally has no manual
trigger: a tag push is the only event that can publish binaries, Homebrew
formulae, and container images.

The verification job runs the Go tests and `govulncheck` before any publishing
job starts. The vulnerability scan is fail-closed by default.

## Emergency vulnerability-scan bypass

Use the bypass only when `govulncheck` itself or its live vulnerability database
is unavailable, or when maintainers have documented a confirmed false positive
that blocks an urgent release. Never use it to publish a release with an
unresolved reachable vulnerability.

1. Record the reason and maintainer approval in the release issue or pull
   request, and confirm no other release is in progress.
2. In **Settings → Secrets and variables → Actions → Variables**, set the
   repository variable `ALLOW_VULN_BYPASS_FOR_TAG` to the exact release tag,
   for example `v0.18.4`.
3. Re-run the failed Release workflow for that tag. A different tag, an empty
   value, or any typo keeps the scan enabled.
4. Confirm the verification log contains the explicit bypass warning and retain
   the workflow URL in the incident record.
5. Delete `ALLOW_VULN_BYPASS_FOR_TAG` immediately after the release run
   completes. The tag-scoped value prevents a concurrent release with another
   tag from inheriting the bypass.

Every Go binary retains its compiler version in the standard Go build metadata;
use `go version -m <binary>` when auditing a downloaded release artifact.

## Fork desktop release (slovx2/multica)

Desktop releases in this fork use the manually dispatched
[Fork Desktop Release](workflows/fork-desktop-release.yml) workflow on GitHub-hosted
runners. The upstream tag-triggered Release workflow described above is separate.
Use GitHub Actions for desktop releases unless a local release is explicitly requested.

Configure these repository Actions secrets before running the workflow:

| Secret | Value |
| --- | --- |
| `CSC_LINK` | Base64-encoded Developer ID Application P12, including its private key |
| `CSC_KEY_PASSWORD` | Password protecting that P12 |
| `APPLE_ID` | Apple account associated with the developer team |
| `APPLE_APP_SPECIFIC_PASSWORD` | Dedicated app-specific password for notarization |
| `APPLE_TEAM_ID` | Developer team ID matching the signing certificate |

The workflow checks all five secrets before creating a release. macOS builds
receive the signing credentials, require Developer ID signing, and enable
notarization. Before upload, both x64 and arm64 app bundles must pass signature,
signing-team, and stapled-ticket checks. Windows packaging does not receive Apple
credentials and remains unsigned. Never commit credentials to the repository.

1. Create and push a new fork tag such as `v0.6.1.2` at the intended source commit.
   The tag must already exist; CI maps it to desktop SemVer `0.6.1-2` locally.
2. Dispatch `fork-desktop-release.yml` from `main` with that tag, for example:
   `gh workflow run fork-desktop-release.yml --repo slovx2/multica --ref main -f tag=v0.6.1.2`.
3. Inspect both platform jobs and the macOS verification step before announcing
   the release. Verify the DMG/ZIP installers and architecture-specific update feeds
   on the fork's release page, then smoke-test installation on both Mac architectures.

Each platform uploads into a draft; only after both finish does the workflow
publish the release as latest. A failed run can be rerun while the release remains
a draft. Published releases cannot be overwritten; always use a new tag.
Credential configuration and workflow validation alone do not establish that a
real build or Apple notarization has succeeded. See [fork update behavior and
existing-client upgrade requirements](FORK_DESKTOP_RELEASE.md).
