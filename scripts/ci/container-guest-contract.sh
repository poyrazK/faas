#!/usr/bin/env bash
# Real Linux process contracts. Source-derived selection and strict verdict
# prevent missing controller/credential prerequisites from becoming a green skip.
set -Eeuo pipefail

[[ "$(uname -s)" == Linux ]] || { echo 'container guest contracts require Linux' >&2; exit 1; }
[[ "${EUID}" -eq 0 ]] || { echo 'container guest contracts require root for credential transitions' >&2; exit 1; }
: "${FAAS_TEST_CGROUP_PARENT:?set FAAS_TEST_CGROUP_PARENT to a cgroup v2 parent with memory/cpu delegated}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source scripts/ci/native-e2e-verdict.sh

files=(
  guest/init/process_identity_linux_test.go
  guest/init/healthcheck_runtime_linux_test.go
  guest/init/cgroup_partition_linux_test.go
  guest/init/cgroup_launch_metal_test.go
  guest/init/scratch_metal_test.go
)
for file in "${files[@]}"; do
  [[ -r "${file}" ]] || { echo "container guest contract file missing: ${file}" >&2; exit 1; }
  grep -qE '^func Test[A-Za-z0-9_]+\(' "${file}" || {
    echo "container guest contract file selects no tests: ${file}" >&2; exit 1;
  }
done
test_names="$(grep -hoE '^func Test[A-Za-z0-9_]+\(' "${files[@]}" | sed -E 's/^func //; s/\($//' | sort -u)"
tests=()
while IFS= read -r test_name; do tests+=("${test_name}"); done <<<"${test_names}"
[[ "${#tests[@]}" -gt 0 ]] || { echo 'container guest contracts selected no tests' >&2; exit 1; }
run_regex="^($(printf '%s\n' "${tests[@]}" | paste -sd'|' -))$"
log="$(mktemp)"
trap 'rm -- "${log}"' EXIT

set +e
"${GO:-go}" test -tags metal ./guest/init -count=1 -timeout=2m -v -run "${run_regex}" 2>&1 | tee "${log}"
test_rc="${PIPESTATUS[0]}"
set -e
native_e2e_lane_verdict "${log}" container-guest-contract "${tests[@]}" || test_rc=1
exit "${test_rc}"
