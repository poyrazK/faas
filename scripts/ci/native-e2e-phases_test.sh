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


# 8. Lanes. A lane is hand-picked, so the only way it can silently shrink is a
#    name that no longer matches a metal test; the assert must catch that, and
#    the beta definition must stay what it is.
native_e2e_assert_lanes "${repo_root}" || fail "a lane names a test that is not in the metal suite"
smoke_tests="$(native_e2e_lane_tests smoke "${repo_root}")"
for must in TestSourceDeployWakeMetal TestDeployWakeMetal \
  TestSec11_MemoryMaxFenceEnforced_CrossProcess TestSec11_SeccompFilterEnforced_CrossProcess; do
  printf '%s\n' "${smoke_tests}" | grep -qx "${must}" ||
    fail "smoke lane lost ${must}; it no longer exercises the beta path"
done
# Fixture checks ride along (no microVM), so a broken fixture tree is reported
# before anything boots.
for t in $(native_e2e_phase_tests fixtures "${repo_root}"); do
  printf '%s\n' "${smoke_tests}" | grep -qx "${t}" || fail "smoke lane lost fixtures test ${t}"
done
# And it must stay SMALL: the lane is the answer to the hour-long matrix.
n="$(printf '%s\n' "${smoke_tests}" | grep -c .)"
[[ "${n}" -le 12 ]] || fail "smoke lane has ${n} tests; it is growing back into the full matrix"
# The assert must actually bite: a bogus name fails it.
( NATIVE_E2E_SMOKE_TESTS+=(TestDoesNotExistAnywhere); native_e2e_assert_lanes "${repo_root}" ) 2>/dev/null &&
  fail "native_e2e_assert_lanes accepted a lane naming a nonexistent test"
native_e2e_is_lane smoke || fail "smoke is not recognised as a lane"
native_e2e_is_lane build && fail "a phase was recognised as a lane"

# 9. The runner's selector gate must accept every phase AND every lane, and
#    reject anything else. Run 35155683638 dispatched lane=smoke and the
#    runner died with "unknown phase smoke" before running a single test.
for sel in "${NATIVE_E2E_PHASES[@]}" "${NATIVE_E2E_LANES[@]}"; do
  native_e2e_is_selector "${sel}" || fail "runner selector gate rejects ${sel}"
done
native_e2e_is_selector nonsense && fail "runner selector gate accepted a bogus name"
grep -q 'native_e2e_is_selector "${phase}"' "${repo_root}/scripts/ci/run-native-e2e.sh" ||
  fail "run-native-e2e.sh does not validate FAAS_E2E_PHASE with native_e2e_is_selector; a lane would be rejected as an unknown phase"

# 10. The stale-jail reaper removes app-instance chroots as well as build ones,
#     and nothing outside firecracker-v*/. Two app chroots that survived a node
#     reboot blocked smoke run 35206846279 before a single test ran.
jail_tmp="$(mktemp -d)"
mkdir -p "${jail_tmp}/firecracker-v1.7.0-x86_64/645b161d-79ef-4a24-adcb-8c96a48e57b1/root" \
         "${jail_tmp}/firecracker-v1.7.0-x86_64/build-01a0ac83/root" \
         "${jail_tmp}/keep-me"
# shellcheck source=scripts/ci/native-e2e-reap.sh
source "${repo_root}/scripts/ci/native-e2e-reap.sh"
reap_stale_jails "${jail_tmp}"
[[ ! -e "${jail_tmp}/firecracker-v1.7.0-x86_64/645b161d-79ef-4a24-adcb-8c96a48e57b1" ]] ||
  fail "reap_stale_jails left an app-instance chroot; the next run's pre-flight leakcheck would refuse to start"
[[ ! -e "${jail_tmp}/firecracker-v1.7.0-x86_64/build-01a0ac83" ]] || fail "reap_stale_jails left a build chroot"
[[ -d "${jail_tmp}/keep-me" ]] || fail "reap_stale_jails removed something outside firecracker-v*/"
rm -rf "${jail_tmp}"
grep -qE '^reap_test_microvms$' "${repo_root}/scripts/ci/run-native-e2e.sh" ||
  fail "run-native-e2e.sh does not reap before the pre-flight leakcheck; a previous run's leftovers block this one"

echo "native e2e phase contracts OK (${#NATIVE_E2E_PHASES[@]} phases, ${#NATIVE_E2E_LANES[@]} lane)"
