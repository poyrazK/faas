#!/usr/bin/env bash
# Verdict rules for the native e2e gate, in their own file so they can be
# exercised against synthetic `go test -v` output by run-native-e2e_test.sh.
# Inline in the runner they would only ever be "verified" by grepping the
# runner for its own strings, which is not a check.
#
# Sourced by run-native-e2e.sh. Defines:
#   NATIVE_E2E_REQUIRED_TESTS — the chain that makes this a platform gate
#   native_e2e_verdict <log>  — prints the tally, returns non-zero on a
#                               vacuous or incomplete run

# The tests that must actually execute. Each needs KVM, the kernel, the builder
# base and Postgres, so any of them SKIPPING means a fixture regressed — which
# the pass/skip tally alone would report as a smaller green run. Names can only
# leave this list by editing it, which is the point.
NATIVE_E2E_REQUIRED_TESTS=(
  TestDeployWakeMetal
  TestSourceDeployWakeMetal
  TestBuildMetal
  TestWakeTimelineMetal
  TestDeployHealthcheckMetal
  TestCatalogRuntimeParityMetal
  TestSec11_MemoryMaxFenceEnforced_CrossProcess
  TestSec11_SeccompFilterEnforced_CrossProcess
)

# native_e2e_verdict reports the run and decides whether it counts as a gate.
# It never inspects the `go test` exit status — the caller keeps that — so a
# suite that failed loudly and a suite that skipped itself green are judged
# separately.
native_e2e_verdict() {
  local log="$1"
  local rc=0
  local passed skipped failed required

  if [[ ! -r "${log}" ]]; then
    echo "native e2e: test log is unreadable: ${log}" >&2
    return 1
  fi

  passed="$(grep -cE '^--- PASS: ' "${log}" || true)"
  skipped="$(grep -cE '^--- SKIP: ' "${log}" || true)"
  failed="$(grep -cE '^--- FAIL: ' "${log}" || true)"
  echo "native e2e: ./cmd/e2e — ${passed} passed, ${skipped} skipped, ${failed} failed"

  if [[ "${skipped}" -gt 0 ]]; then
    echo "native e2e: skipped tests (each names the fixture it wants):"
    grep -E '^--- SKIP: ' "${log}" | sed 's/^/  /'
  fi

  # A green check must mean tests executed. This is the exact failure mode of
  # the retired self-hosted `metal` job, which looked dormant rather than
  # broken for 100 consecutive dispatches.
  if [[ "${passed}" -eq 0 ]]; then
    echo "native e2e: no test executed; the fixtures, the DSN or the build tag are wrong" >&2
    rc=1
  fi

  for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
    if grep -qE "^--- SKIP: ${required}( |\$)" "${log}"; then
      echo "native e2e: required test ${required} SKIPPED; its fixture is missing" >&2
      rc=1
      continue
    fi
    if ! grep -qE "^--- (PASS|FAIL): ${required}( |\$)" "${log}"; then
      echo "native e2e: required test ${required} did not run at all" >&2
      rc=1
    fi
  done
  if [[ "${rc}" -ne 0 ]]; then
    echo "native e2e: the required end-to-end chain did not execute in full" >&2
    return "${rc}"
  fi

  echo "native e2e: required chain executed; ${passed} passed, ${skipped} skipped"
  return 0
}
