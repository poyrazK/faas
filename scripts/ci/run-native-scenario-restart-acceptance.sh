#!/usr/bin/env bash
# Run candidate CLI scenarios against a designated native acceptance fleet.
# Owns only newly-created developer workspaces; never changes fleet settings.
set -Eeuo pipefail
umask 077
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
evidence_root="${FAAS_SCENARIO_EVIDENCE_ROOT:-${repo_root}/scenario-restart-native-evidence}"
mkdir -p -- "${evidence_root}"
evidence="$(mktemp -d "${evidence_root}/run-XXXXXXXX")"
echo "Native scenario evidence: ${evidence}"
python3 "${repo_root}/scripts/ci/scenario-restart-native.py" --repo "${repo_root}" --out "${evidence}" --check-runner
command -v node >/dev/null
command -v openssl >/dev/null
command -v flock >/dev/null
go_tool="${GO:-go}"
command -v "${go_tool}" >/dev/null
# Share the existing native host lock to avoid competing with builder/e2e drills.
exec 9>/var/lock/faas-builder-acceptance.lock
flock -w 30 9
npm --prefix "${repo_root}/examples/scenario-http-write/payment-postgres" ci --ignore-scripts --no-audit --no-fund
workdir="$(mktemp -d)"
trap 'rm -rf -- "${workdir}"' EXIT
cd "${repo_root}"
"${go_tool}" build -trimpath -o "${workdir}/gregale" ./cmd/gregale
python3 scripts/ci/scenario-restart-native.py --repo "${repo_root}" --out "${evidence}" --cli "${workdir}/gregale"
