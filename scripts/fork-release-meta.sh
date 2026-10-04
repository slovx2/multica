#!/usr/bin/env bash
set -euo pipefail

# Derive every artifact's identity from the checked-out source, not the workflow
# revision or a ref that might move while the image and CLI jobs are running.
source_dir=${1:?usage: fork-release-meta.sh SOURCE_DIRECTORY}
sha=$(git -C "$source_dir" rev-parse --verify 'HEAD^{commit}')
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo 'Expected a full Git SHA-1' >&2; exit 1; }
printf 'sha=%s\ntag=v0.6.1-slovx2-%s\ndate=%s\n' \
  "$sha" "${sha:0:9}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
