# Release runbook

## Normal upstream release

The upstream Release workflow skips all jobs outside the `multica-ai` owner.
Use the fork workflows below for `slovx2/multica`.

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

## Fork images and CLI (slovx2/multica)

Build fork artifacts on GitHub Actions, after the workflow PR has been reviewed
and merged. Do not build release images or installers locally.
[Fork Image and CLI Release](workflows/fork-image-release.yml) is manually
dispatched from `main`; its `ref` input defaults to `main` and can also name a
reviewed commit or tag:

```bash
gh workflow run fork-image-release.yml --repo slovx2/multica --ref main -f ref=main
```

The workflow resolves the source once to a full SHA. All three artifacts use
that SHA, with version `v0.6.1-slovx2-<first 9 SHA characters>`:

- `ghcr.io/slovx2/multica-backend:<version>` (linux/amd64, existing `Dockerfile`)
- `ghcr.io/slovx2/multica-web:<version>` (linux/amd64, existing `Dockerfile.web`)
- Actions artifact `multica-cli-<version>-linux-amd64`, retained for 30 days.
  It contains a tarball with `multica`, its version output, LICENSE and NOTICE,
  plus `SHA256SUMS` for the archive. The workflow runs `multica version` and
  checks the embedded version and commit before uploading it.

Only the image job receives `packages: write`; it authenticates with the
repository's `GITHUB_TOKEN`. No Docker Hub credential or new secret is needed.
Image digests and the source SHA are recorded in the job summaries. Rebuilding
a commit uses the same tag, but floating base images/toolchains can change its
digest; record the successful run and digests when deploying.

### First publish: make both packages public

GHCR packages are private on first publication, even for a public repository.
After the first successful run, open the `multica-backend` and `multica-web`
packages under the `slovx2` GitHub account, then **Package settings → Change
visibility → Public**. If the operator cannot do this, ask Kal to change both
packages in GitHub; stop before deployment. Do not work around the requirement
with a server login or a locally transferred image. See the
[GitHub container registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

### Verify and deploy

1. Confirm the entire run succeeded, with both image jobs and the CLI artifact.
   Download the CLI artifact with `gh run download <run-id> --repo slovx2/multica
   --name multica-cli-<version>-linux-amd64`, verify `sha256sum -c SHA256SUMS`,
   extract the tarball, and run `./multica version` on Linux amd64. This does
   not install or replace the daemon's CLI.
2. On sg-prod, use an empty temporary Docker config to prove anonymous access:

   ```bash
   docker_config=$(mktemp -d)
   docker --config "$docker_config" pull ghcr.io/slovx2/multica-backend:<version>
   docker --config "$docker_config" pull ghcr.io/slovx2/multica-web:<version>
   rmdir "$docker_config"
   ```

3. Before editing `/root/multica/docker-compose.selfhost.yml`, back up the
   database, `.env`, and compose in `/root/multica/backups`; validate the dump
   with `pg_restore --list` and retain the latest four complete backup sets.
   Record the previous images and a rollback command in the deployment issue.
4. Change only the backend and frontend image references to the two GHCR
   images. Validate compose, then recreate those services with
   `docker compose -f docker-compose.selfhost.yml up -d --no-deps backend frontend`.
   Check `/health` for the expected commit, `/healthz` for database/migration
   readiness, and the public login page. If validation fails, restore the
   backed-up compose and recreate the previous images; evaluate database
   compatibility separately if the selected commit introduces migrations.

This workflow publishes artifacts only; deployment remains a separate approved
operation. Updating the host daemon CLI is also a separate operation.

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
