#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT

# Test against real Git history: the selected source differs from both the
# workflow checkout and the tip of main, as it does for historical releases.
git init -q -b main "$fixture/source with spaces"
source_dir="$fixture/source with spaces"
git -C "$source_dir" -c user.name=Test -c user.email=test@example.invalid \
  commit -q --allow-empty -m first
first=$(git -C "$source_dir" rev-parse HEAD)
git -C "$source_dir" -c user.name=Test -c user.email=test@example.invalid \
  tag -a historical -m historical
git -C "$source_dir" -c user.name=Test -c user.email=test@example.invalid \
  commit -q --allow-empty -m second
second=$(git -C "$source_dir" rev-parse HEAD)

check_metadata() {
  local expected=$1 actual
  actual=$(bash "$script_dir/fork-release-meta.sh" "$source_dir")
  grep -qx "sha=$expected" <<< "$actual"
  grep -qx "tag=v0.6.1-slovx2-${expected:0:9}" <<< "$actual"
  grep -Eq '^date=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$' <<< "$actual"
  [[ $(wc -l <<< "$actual") -eq 3 ]]
}

check_metadata "$second"
git -C "$source_dir" checkout -q --detach historical
check_metadata "$first"
# An advanced branch must not change metadata for the already resolved SHA.
git -C "$source_dir" checkout -q main
git -C "$source_dir" -c user.name=Test -c user.email=test@example.invalid \
  commit -q --allow-empty -m third
git -C "$source_dir" checkout -q --detach "$first"
check_metadata "$first"

mkdir "$fixture/not-a-repo"
if bash "$script_dir/fork-release-meta.sh" "$fixture/not-a-repo" > "$fixture/output" 2>/dev/null; then
  echo 'Non-repository source unexpectedly accepted' >&2
  exit 1
fi
test ! -s "$fixture/output"
git init -q "$fixture/empty-repo"
if bash "$script_dir/fork-release-meta.sh" "$fixture/empty-repo" > "$fixture/output" 2>/dev/null; then
  echo 'Empty repository unexpectedly accepted' >&2
  exit 1
fi
test ! -s "$fixture/output"
echo 'PASS: current branch, historical tag, pinned SHA, paths with spaces, invalid and empty source'
