#!/usr/bin/env bash
# ADR-20261018000100: a dedicated native host and disposable fixture cluster only.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
exec python3 "$repo/scripts/ci/rehearsal-native.py" --repo "$repo" "$@"
