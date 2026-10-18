#!/usr/bin/env bash
# Transfer a clean, ordinary Git checkout; qualification rejects archive-only
# sources, worktree .git files and dirty/mismatched executable VCS metadata.
set -euo pipefail
umask 077
[[ $# == 2 && $1 =~ ^[0-9a-f]{40}$ ]] || { echo 'usage: package-binding-native-source.sh SHA OUTPUT' >&2; exit 1; }
source_sha="$1" output="$2"
[[ ! -e "$output" && ! -L "$output" ]] || { echo 'Source bundle already exists' >&2; exit 1; }
[[ "$(git rev-parse HEAD)" == "$source_sha" && -z "$(git status --porcelain)" ]] || {
  echo 'Binding source transfer requires the exact clean checkout' >&2; exit 1;
}
bundle_tmp="$(mktemp -d)"
trap 'rm -rf "$bundle_tmp"' EXIT
# A fresh clone does not copy the checkout action's credential configuration,
# hooks or index modifications. --no-hardlinks keeps the transfer self-contained.
git clone --quiet --no-hardlinks --no-checkout . "$bundle_tmp/source"
git -C "$bundle_tmp/source" checkout --quiet --detach "$source_sha"
git -C "$bundle_tmp/source" remote remove origin
[[ -d "$bundle_tmp/source/.git" && -z "$(git -C "$bundle_tmp/source" status --porcelain)" ]]
tar -C "$bundle_tmp" -czf "$bundle_tmp/source.tar.gz" source
# Do not replace earlier evidence, including dangling symlinks.
(set -C; cat "$bundle_tmp/source.tar.gz" > "$output")
