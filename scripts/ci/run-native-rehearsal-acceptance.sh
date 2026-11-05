#!/usr/bin/env bash
# ADR-20261018000100: a dedicated native host and disposable fixture cluster only.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
# Capability flags (ADR-20261023115741) must release what this run exercises.
python3 "$repo/scripts/ci/require-capabilities.py" --api "${FAAS_API:?set the acceptance API URL}" --token-env FAAS_TOKEN scenario-tests
exec python3 "$repo/scripts/ci/rehearsal-native.py" --repo "$repo" "$@"
