#!/usr/bin/env bash
# Strict first-slice migration qualification; no fallback to heartbeat smoke.
set -Eeuo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
exec python3 "${repo_root}/scripts/ci/native-m9-gate.py" "$@"
