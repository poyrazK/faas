#!/usr/bin/env bash
# Exercise the native e2e gate's verdict rules against synthetic `go test -v`
# output, and pin the production-shape contracts of the wrapper around them:
# the shell -> make -> go test expansion, the absence of a -run filter, the
# Postgres hard-fail that keeps a database-backed suite from skipping itself
# green, and the host-state restoration.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${repo_root}/scripts/ci/run-native-e2e.sh"
verdict="${repo_root}/scripts/ci/native-e2e-verdict.sh"

fail() {
  echo "native e2e contract: $*" >&2
  exit 1
}

[[ -x "${runner}" ]] || fail "wrapper is not executable: ${runner}"
[[ -r "${verdict}" ]] || fail "verdict rules are missing: ${verdict}"

# ---------------------------------------------------------------------------
# The verdict rules, driven with synthetic logs.
# ---------------------------------------------------------------------------
# shellcheck source=scripts/ci/native-e2e-verdict.sh
source "${verdict}"

[[ "${#NATIVE_E2E_REQUIRED_TESTS[@]}" -ge 8 ]] ||
  fail "the required-test contract shrank to ${#NATIVE_E2E_REQUIRED_TESTS[@]} tests"

for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
  # A typo'd requirement could never match a PASS line: the gate would fail for
  # a reason that is not a product defect, and the obvious "fix" would be to
  # delete the requirement.
  grep -rqE "^func ${required}\(" "${repo_root}/cmd/e2e" ||
    fail "required test ${required} does not exist in cmd/e2e"
done

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

all_pass_log() {
  local out="$1"
  : > "${out}"
  for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
    printf -- '--- PASS: %s (1.00s)\n' "${required}" >> "${out}"
  done
  printf -- '--- PASS: TestSomethingElse (0.01s)\n' >> "${out}"
  printf -- 'ok  \tgithub.com/onebox-faas/faas/cmd/e2e\t42.0s\n' >> "${out}"
}

# 1. A complete run passes.
all_pass_log "${work}/green.log"
native_e2e_verdict "${work}/green.log" >"${work}/green.out" 2>&1 ||
  fail "a complete run was rejected: $(cat "${work}/green.out")"
grep -Fq 'required chain executed' "${work}/green.out" ||
  fail "a complete run did not report the required chain"

# 2. A run that executed nothing fails — the retired self-hosted metal job's
#    exact failure mode, where 100 dispatches looked dormant rather than broken.
printf -- 'testing: warning: no tests to run\nPASS\n' > "${work}/empty.log"
if native_e2e_verdict "${work}/empty.log" >"${work}/empty.out" 2>&1; then
  fail "a run with zero executed tests was accepted"
fi
grep -Fq 'no test executed' "${work}/empty.out" ||
  fail "the zero-executed failure does not say so: $(cat "${work}/empty.out")"

# 3. A run where a required test SKIPPED fails and names it. This is the
#    Postgres/kernel/builder-base regression shape: hundreds of other tests
#    still pass, so the tally alone stays plausible.
all_pass_log "${work}/skip.log"
victim="${NATIVE_E2E_REQUIRED_TESTS[0]}"
grep -v -- "--- PASS: ${victim} " "${work}/skip.log" > "${work}/skip.tmp"
printf -- '--- SKIP: %s (0.00s)\n' "${victim}" >> "${work}/skip.tmp"
mv "${work}/skip.tmp" "${work}/skip.log"
if native_e2e_verdict "${work}/skip.log" >"${work}/skip.out" 2>&1; then
  fail "a run that skipped required test ${victim} was accepted"
fi
grep -Fq "required test ${victim} SKIPPED" "${work}/skip.out" ||
  fail "the skipped required test was not named: $(cat "${work}/skip.out")"

# 4. A run where a required test is absent entirely fails — the shape a stray
#    -run filter or a deleted test would produce.
all_pass_log "${work}/absent.log"
grep -v -- "--- PASS: ${victim} " "${work}/absent.log" > "${work}/absent.tmp"
mv "${work}/absent.tmp" "${work}/absent.log"
if native_e2e_verdict "${work}/absent.log" >"${work}/absent.out" 2>&1; then
  fail "a run missing required test ${victim} was accepted"
fi
grep -Fq "required test ${victim} did not run at all" "${work}/absent.out" ||
  fail "the absent required test was not named: $(cat "${work}/absent.out")"

# 5. A required test that FAILED is a product defect, not a vacuous gate: the
#    verdict must not mask it, and must not double-report it either.
all_pass_log "${work}/red.log"
grep -v -- "--- PASS: ${victim} " "${work}/red.log" > "${work}/red.tmp"
printf -- '--- FAIL: %s (1.00s)\n' "${victim}" >> "${work}/red.tmp"
mv "${work}/red.tmp" "${work}/red.log"
native_e2e_verdict "${work}/red.log" >"${work}/red.out" 2>&1 ||
  fail "the verdict rejected a run whose required test failed; the go test exit status owns that"
grep -Fq '1 failed' "${work}/red.out" ||
  fail "a failing required test is not reported in the tally: $(cat "${work}/red.out")"

# 6. Subtest lines must not satisfy a requirement: `go test -v` indents them,
#    and a parent that never ran cannot be inferred from a child that did.
{
  printf -- '    --- PASS: %s/subtest (0.10s)\n' "${victim}"
  printf -- '--- PASS: TestSomethingElse (0.01s)\n'
} > "${work}/subtest.log"
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]:1}"; do
  printf -- '--- PASS: %s (1.00s)\n' "${required}" >> "${work}/subtest.log"
done
if native_e2e_verdict "${work}/subtest.log" >"${work}/subtest.out" 2>&1; then
  fail "an indented subtest line satisfied the requirement for ${victim}"
fi

# ---------------------------------------------------------------------------
# The wrapper around those rules.
# ---------------------------------------------------------------------------
grep -Fq 'native_e2e_verdict "${e2e_log}"' "${runner}" ||
  fail "the wrapper does not apply the verdict rules to its test log"
grep -Fq 'source "${repo_root}/scripts/ci/native-e2e-verdict.sh"' "${runner}" ||
  fail "the wrapper does not load the verdict rules"

# ---------------------------------------------------------------------------
# The run set: derived from source, never hand-listed.
# ---------------------------------------------------------------------------
# Execute the SAME derivation the runner uses, against this tree. This is what
# replaced the old "RUN_ARGS must contain no -run filter" rule: the gate does
# filter now (running all ~330 e2e tests starved the metal ones on the 4-vCPU
# node), but the filter is generated from the build tag, so it cannot be
# narrowed to a hand-picked test without failing these checks.
metal_tests="$(native_e2e_metal_tests "${repo_root}")"
[[ -n "${metal_tests}" ]] || fail "the derivation found no metal-tagged tests in cmd/e2e"
metal_count="$(printf '%s\n' "${metal_tests}" | wc -l | tr -d ' ')"
[[ "${metal_count}" -ge 15 ]] ||
  fail "only ${metal_count} metal-tagged tests derived; the gate has shrunk or the derivation broke"

# Every required test must be derivable, or it would silently stop being run.
for required in "${NATIVE_E2E_REQUIRED_TESTS[@]}"; do
  printf '%s\n' "${metal_tests}" | grep -qx "${required}" ||
    fail "required test ${required} is not in the metal-tagged set (lost its //go:build metal tag?)"
done

# The derivation must actually key on the build tag, not on a filename pattern:
# metal tests live in files both with and without a _metal_ infix.
grep -Fq "grep -l '^//go:build metal'" "${repo_root}/scripts/ci/native-e2e-verdict.sh" ||
  fail "the run set is no longer derived from the //go:build metal tag"

# The runner must build its filter from that derivation and refuse an empty set.
grep -Fq 'native_e2e_metal_tests "${repo_root}"' "${runner}" ||
  fail "the wrapper does not derive its run set from source"
grep -Fq 'no metal-tagged tests found in cmd/e2e' "${runner}" ||
  fail "the wrapper does not fail when the derived run set is empty"
grep -Fq 'is not in the metal-tagged set' "${runner}" ||
  fail "the wrapper does not verify every required test is in the derived set"

expanded="$(
  make -n -C "${repo_root}" GO=/usr/bin/true PKGS=./cmd/e2e/... \
    RUN_ARGS="-timeout=75m -v -run ^(TestExample)\$" test-metal
)"
grep -Fq -- '/usr/bin/true test -tags metal -race -count=1 -timeout=75m -v -run' <<<"${expanded}" || {
  printf '%s\n' "${expanded}" >&2
  fail "the invocation expanded incorrectly"
}
# The metal build tag is what makes this a platform gate instead of a rerun of
# the pure-Go e2e shard CI already has.
grep -Fq -- '-tags metal' <<<"${expanded}" || fail "the gate does not use the metal build tag"
grep -Fq 'PKGS=./cmd/e2e/...' "${runner}" ||
  fail "the wrapper no longer targets the ./cmd/e2e package"

# Postgres. pgtest.Open calls t.Skip when the cluster is unreachable, so
# without these the gate reports a green run of ~zero database-backed tests.
grep -Fq 'pg_isready' "${runner}" || fail "the wrapper does not probe Postgres before running"
grep -Fq 'Postgres is not reachable at the configured DSN' "${runner}" ||
  fail "the wrapper does not hard-fail on an unreachable Postgres"
# Reachable is not usable: pgtest creates a schema per test and installs citext.
grep -Fq 'create extension if not exists citext' "${runner}" ||
  fail "the wrapper does not prove the DSN can create schemas and citext"
# Must be a refusal on a non-comment line; a comment mentioning the opt-out
# does not disable it.
grep -vE '^[[:space:]]*#' "${runner}" | grep -Fq 'unset FAAS_SKIP_PG_TESTS' ||
  fail "the wrapper does not clear FAAS_SKIP_PG_TESTS (a comment mentioning it does not count)"
# An env file that exists but yields an empty DSN must be fatal, not a silent
# fall back to the default cluster. Hit for real on 2026-09-12: the DSN holds an
# unescaped '&', and written unquoted the shell backgrounds the assignment in a
# subshell so the variable arrives empty.
grep -Fq 'FAAS_E2E_DATABASE_URL is empty after sourcing it' "${runner}" ||
  fail "the wrapper silently falls back to the default DSN when the host env file yields an empty one"
# Postgres is the one service the suite needs up.
if grep -E '^candidate_services=|^[[:space:]]+faas-' "${runner}" | grep -q 'postgres'; then
  fail "the wrapper stops Postgres; the suite needs it running"
fi

# Host safety: the wrapper stops production daemons, so every exit path has to
# restore them and leave the node clean.
grep -Fq 'leakcheck.sh' "${runner}" || fail "the wrapper does not run the host leak check"
grep -Fq 'trap cleanup EXIT HUP INT TERM' "${runner}" ||
  fail "the wrapper does not restore host state on every exit path"
grep -Fq '/var/lock/faas-builder-acceptance.lock' "${runner}" ||
  fail "the wrapper does not serialize with the other native gates on this node"
grep -Fq '/etc/faas/builder-acceptance-host' "${runner}" ||
  fail "the wrapper does not require the acceptance-host designation marker"

echo "native e2e wrapper contracts OK"
