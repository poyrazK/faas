#!/usr/bin/env bash
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
output=""
if [[ "$#" -gt 0 ]]; then
  [[ "$#" -eq 2 && "$1" == "--out-dir" ]] || { echo 'Usage: scripts/test-data-api-packaging.sh [--out-dir NEW_DIR]' >&2; exit 1; }
  output="$2"
fi
work="$(mktemp -d "${TMPDIR:-/tmp}/gregale-packaging-check.XXXXXX")"
trap 'rm -rf "$work"' EXIT
output="${output:-$work/bundle}"
# The prerelease label is validation metadata, not a release tag or publication.
version="v0.0.0-data-api.$(git rev-parse --short=12 HEAD)"
node scripts/build-data-api-bundle.mjs --version "$version" --out-dir "$output"
node scripts/build-data-api-bundle.mjs --version "$version" --out-dir "$work/rebuild"
cmp "$output/DATA-API-SHA256SUMS" "$work/rebuild/DATA-API-SHA256SUMS"
cmp "$output/data-api-bundle.json" "$work/rebuild/data-api-bundle.json"
echo 'Repeated CLI/SDK builds produced identical checksums and metadata.'
node scripts/test-data-api-bundle.mjs "$output"
