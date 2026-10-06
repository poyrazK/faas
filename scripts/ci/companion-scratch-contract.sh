#!/usr/bin/env bash
# Exercise production scratch mounts and reject missing/skipped acceptance.
set -Eeuo pipefail

[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || {
  echo 'companion scratch acceptance requires Linux/x86_64' >&2
  exit 1
}
[[ "${EUID}" -eq 0 ]] || {
  echo 'companion scratch acceptance requires root for private mount namespaces' >&2
  exit 1
}
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source scripts/ci/native-e2e-verdict.sh
log="$(mktemp)"
trap 'rm -- "${log}"' EXIT
required_test=TestCompanionScratchCapacityIsolationAndCleanup
set +e
"${GO:-go}" test -tags metal ./guest/init -count=1 -timeout=1m -v -run "^${required_test}$" 2>&1 | tee "${log}"
test_rc="${PIPESTATUS[0]}"
set -e
native_e2e_lane_verdict "${log}" companion-scratch "${required_test}" || test_rc=1
exit "${test_rc}"
