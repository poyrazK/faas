#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check-release-assets.py"
workflow="$repo_root/.github/workflows/release.yml"
fixture="$(mktemp)"
trap 'rm -f "$fixture"' EXIT

python3 "$checker" "$workflow"

sed 's#/tmp/gregale-build/CLI-SHA256SUMS#/tmp/gregale-build/SHA256SUMS#' "$workflow" > "$fixture"
if python3 "$checker" "$fixture" >/dev/null 2>&1; then
  echo "checker accepted duplicate SHA256SUMS release assets" >&2
  exit 1
fi

echo "release asset-name checker regression: PASS"
