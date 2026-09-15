#!/usr/bin/env bash
# Contracts for the phase partition. The gate's whole value rests on the run
# set being derived from source and covering every metal test; phases add a
# second way to lose a test (assign it nowhere) and a second way to double-run
# one (assign it twice). Both are silent without these checks.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
phases="${repo_root}/scripts/ci/native-e2e-phases.sh"
verdict="${repo_root}/scripts/ci/native-e2e-verdict.sh"

fail() { echo "native e2e phases: $*" >&2; exit 1; }

[[ -r "${phases}" ]] || fail "missing ${phases}"
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source "${verdict}"
# shellcheck source=scripts/ci/native-e2e-phases.sh
source "${phases}"

# 1. The real tree must be an exact partition.
native_e2e_assert_phase_partition "${repo_root}" ||
  fail "the phases are not a partition of the metal suite on this tree"

# 2. Phase count sanity: a collapse to one phase would restore the monolith
#    this split exists to remove.
[[ "${#NATIVE_E2E_PHASES[@]}" -ge 5 ]] ||
  fail "only ${#NATIVE_E2E_PHASES[@]} phases; the split has collapsed"

# 3. Every phase must select at least one test. An empty phase reports "ok"
#    having executed nothing — the retired metal job's failure mode.
for phase in "${NATIVE_E2E_PHASES[@]}"; do
  n="$(native_e2e_phase_tests "${phase}" "${repo_root}" | grep -c . || true)"
  [[ "${n}" -gt 0 ]] || fail "phase ${phase} selects no tests"
done

# 4. Every required test must live in some phase, or the gate would require a
#    test it never runs.
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
  found=0
  for phase in "${NATIVE_E2E_PHASES[@]}"; do
    if native_e2e_phase_tests "${phase}" "${repo_root}" | grep -qx "${required}"; then
      found=1
      break
    fi
  done
  [[ "${found}" -eq 1 ]] || fail "required test ${required} is in no phase"
done

# 5. The partition assert must actually FAIL on an unassigned metal test.
#    A guard that cannot fail is not a guard — this gate has shipped one before.
probe="$(mktemp -d)"
trap 'rm -rf "${probe}"' EXIT
mkdir -p "${probe}/cmd/e2e"
printf '//go:build metal\n\npackage e2e_test\n\nfunc TestFixtureShape(t *testing.T) {}\n' \
  > "${probe}/cmd/e2e/fixtures_meta_test.go"
printf '//go:build metal\n\npackage e2e_test\n\nfunc TestUnassignedProbe(t *testing.T) {}\n' \
  > "${probe}/cmd/e2e/unassigned_probe_metal_test.go"
if native_e2e_assert_phase_partition "${probe}" >/dev/null 2>&1; then
  fail "the partition assert accepted a metal test assigned to no phase"
fi

# 6. The phase regex must be anchored, or a phase would run every test whose
#    name merely contains one of its own.
rx="$(native_e2e_phase_regex security "${repo_root}")"
[[ "${rx}" == ^\(*\)\$ ]] || fail "phase regex is not anchored: ${rx}"

# 7. A phase that executed nothing must be rejected by the tally.
empty_log="$(mktemp)"
printf 'testing: warning: no tests to run\nPASS\n' > "${empty_log}"
if native_e2e_phase_tally "${empty_log}" probe >/dev/null 2>&1; then
  rm -f "${empty_log}"
  fail "the phase tally accepted a phase that executed no test"
fi
rm -f "${empty_log}"

echo "native e2e phase contracts OK (${#NATIVE_E2E_PHASES[@]} phases)"
