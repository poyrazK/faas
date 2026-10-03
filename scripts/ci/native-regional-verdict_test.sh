#!/usr/bin/env bash
set -Eeuo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${repo_root}/scripts/ci/native-regional-verdict.sh"
stage="$(mktemp -d)"
trap 'rm -rf "${stage}"' EXIT
for test in "${NATIVE_REGIONAL_REQUIRED_TESTS[@]}"; do
  printf '%s\n' "--- PASS: ${test} (1.00s)" >> "${stage}/good.log"
done
native_regional_verdict "${stage}/good.log"
tail -n +2 "${stage}/good.log" > "${stage}/missing.log"
if native_regional_verdict "${stage}/missing.log" >/dev/null 2>&1; then echo 'accepted a missing regional drill' >&2; exit 1; fi
cp "${stage}/good.log" "${stage}/skip.log"
printf '%s\n' '    --- SKIP: TestNativeRegionalJobsAcceptance/worker_loss_retries_on_same_region_peer (0.00s)' >> "${stage}/skip.log"
if native_regional_verdict "${stage}/skip.log" >/dev/null 2>&1; then echo 'accepted a skipped regional drill' >&2; exit 1; fi
printf '%s\n' 'PASS' > "${stage}/empty.log"
if native_regional_verdict "${stage}/empty.log" >/dev/null 2>&1; then echo 'accepted a gate that ran no regional tests' >&2; exit 1; fi
echo 'native regional verdict contracts OK'
