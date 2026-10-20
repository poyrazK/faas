#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${repo_root}/scripts/ci/native-e2e-verdict.sh"
source "${repo_root}/scripts/ci/financial-compute-acceptance.sh"
work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

fail() { echo "financial compute acceptance contract: $*" >&2; exit 1; }

for lane in native postgres; do
  names="$(financial_compute_required_tests "${repo_root}" "${lane}")"
  mapfile -t required <<<"${names}"
  [[ "${#required[@]}" -ge 3 ]] || fail "${lane} lost its required scenarios"
  : > "${work}/green.log"
  for name in "${required[@]}"; do
    printf -- '--- PASS: %s (0.10s)\n' "${name}" >> "${work}/green.log"
  done
  financial_compute_verdict "${repo_root}" "${lane}" "${work}/green.log" >/dev/null || fail "complete ${lane} run rejected"
  victim="${required[0]}"
  for outcome in absent skip fail child-skip; do
    if [[ "${outcome}" == child-skip ]]; then
      cp "${work}/green.log" "${work}/bad.log"
      printf -- '    --- SKIP: %s/missing-fixture (0.00s)\n' "${victim}" >> "${work}/bad.log"
    else
      grep -vF -- "--- PASS: ${victim} " "${work}/green.log" > "${work}/bad.log"
      case "${outcome}" in
        skip) printf -- '--- SKIP: %s (0.00s)\n' "${victim}" >> "${work}/bad.log" ;;
        fail) printf -- '--- FAIL: %s (0.10s)\n' "${victim}" >> "${work}/bad.log" ;;
      esac
    fi
    if financial_compute_verdict "${repo_root}" "${lane}" "${work}/bad.log" >"${work}/verdict.log" 2>&1; then
      fail "accepted ${lane} ${outcome}"
    fi
  done
  printf 'testing: warning: no tests to run\nPASS\n' > "${work}/empty.log"
  if financial_compute_verdict "${repo_root}" "${lane}" "${work}/empty.log" >/dev/null 2>&1; then
    fail "accepted empty ${lane} run"
  fi
done

# Test discovery against a changed source tree, rather than a duplicate list.
mkdir -p "${work}/source/pkg/fcvm"
printf 'func TestMetalFinancialNewScenario(t *testing.T) {}\n' > "${work}/source/pkg/fcvm/financial_compute_added_metal_test.go"
[[ "$(financial_compute_required_tests "${work}/source" native)" == TestMetalFinancialNewScenario ]] || fail "new native source was omitted"
printf -- '--- PASS: TestMetalFinancialOldScenario (0.1s)\n' > "${work}/stale.log"
if financial_compute_verdict "${work}/source" native "${work}/stale.log" >/dev/null 2>&1; then
  fail "stale evidence qualified newly added source"
fi
rm "${work}/source/pkg/fcvm/financial_compute_added_metal_test.go"
if financial_compute_required_tests "${work}/source" native >/dev/null 2>&1; then
  fail "missing source qualified an empty native lane"
fi
echo 'financial compute acceptance contract: PASS'
