#!/usr/bin/env bash
# Qualify the candidate Start CLI against a dedicated, already-installed fleet.
set -Eeuo pipefail
umask 077
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
evidence_root="${FAAS_START_EVIDENCE_ROOT:-${repo_root}/start-native-evidence}"
mkdir -p -- "${evidence_root}"
evidence="$(mktemp -d "${evidence_root}/run-XXXXXXXX")"
echo "Native Start evidence: ${evidence}"
python3 "${repo_root}/scripts/ci/start-native.py" --check-runner
# Capability flags (ADR-20261023115741) must release what this run exercises.
python3 "${repo_root}/scripts/ci/require-capabilities.py" --api "${FAAS_API:?set the acceptance API URL}" --token-env FAAS_TOKEN projects
command -v flock >/dev/null
command -v git >/dev/null
go_tool="${GO:-go}"
command -v "${go_tool}" >/dev/null
# Serialize with the existing native builder/e2e host gates.
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 30 9
workdir="$(mktemp -d)"
trap 'rm -rf -- "${workdir}"' EXIT
cd "${repo_root}"
"${go_tool}" build -trimpath -o "${workdir}/gregale" ./cmd/gregale
"${go_tool}" build -trimpath -o "${workdir}/start-relay" ./scripts/ci/start-relay
python3 scripts/ci/start-native.py --repo "${repo_root}" --out "${evidence}" --cli "${workdir}/gregale" --relay "${workdir}/start-relay"
