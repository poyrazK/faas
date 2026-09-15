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
# Exercise the derivation under the RUNNER's shell options, not this script's.
# run-native-e2e.sh sets `set -Eeuo pipefail`; without that here, a pipeline
# whose last element exits non-zero still looks fine. It did look fine — and the
# gate then died three seconds into a real run with no output, because a
# metal-tagged file declaring no top-level Test func (fixtures_test.go) made the
# inner grep exit 1 and pipefail propagated it out of the command substitution.
#
# The fixture puts such a file LAST in sort order deliberately: that is the only
# ordering that reproduces it, and it is the ordering GNU grep produced on the
# node while the local grep did not.
probe_root="$(mktemp -d)"
mkdir -p "${probe_root}/cmd/e2e"
printf '//go:build metal\n\npackage e2e_test\n\nfunc TestProbeAlpha(t *testing.T) {}\n' \
  > "${probe_root}/cmd/e2e/a_probe_test.go"
printf '//go:build metal\n\npackage e2e_test\n\nfunc helperOnly() {}\n' \
  > "${probe_root}/cmd/e2e/zz_no_tests_test.go"
probe_out="$(bash -c '
  set -Eeuo pipefail
  source "$1"
  native_e2e_metal_tests "$2"
' _ "${verdict}" "${probe_root}" 2>&1)" || {
  rm -rf "${probe_root}"
  fail "the derivation dies under set -Eeuo pipefail when a metal-tagged file declares no tests"
}
[[ "${probe_out}" == "TestProbeAlpha" ]] || {
  rm -rf "${probe_root}"
  fail "the derivation returned '${probe_out}', want just TestProbeAlpha"
}
rm -rf "${probe_root}"

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

# EXECUTE make with a regex carrying the two characters that broke it, and
# assert the filter reaches `go test` byte-for-byte.
#
# RUN_ARGS used to carry the regex. Make expands RUN_ARGS and the shell then
# re-parses it, so `^(TestA|TestB)$` lost its trailing anchor to Make ($ is Make
# syntax) and then died in the shell on the unquoted `(` and `|` with
# "syntax error near unexpected token '('". go test never ran; the gate reported
# 0 passed / 0 skipped / 0 failed (dispatch 34760212826). A `make -n` check
# could not catch it — the breakage is in what the shell does with the expanded
# line, not in the expansion.
probe_bin="$(mktemp -d)/showargs"
printf '#!/bin/sh\nprintf "%%s\\n" "$@"\n' > "${probe_bin}"
chmod +x "${probe_bin}"
probe_args="$(
  RUN_REGEX='^(TestAlpha|TestBeta)$' make -C "${repo_root}" GO="${probe_bin}" \
    PKGS=./cmd/e2e/... RUN_ARGS='-timeout=75m -v' test-metal 2>/dev/null
)"
grep -Fqx -- '-run' <<<"${probe_args}" ||
  fail "make did not pass a -run flag through from RUN_REGEX"
# -F: the pattern IS the regex we are asserting arrived intact, so it must be
# matched literally, not interpreted.
grep -Fqx -- '^(TestAlpha|TestBeta)$' <<<"${probe_args}" || {
  printf '%s\n' "${probe_args}" >&2
  fail "the -run regex was mangled in transit; it must reach go test byte-for-byte"
}
# And with RUN_REGEX unset, no -run flag appears at all — the whole-package
# behaviour every other caller of test-metal relies on.
probe_plain="$(
  make -C "${repo_root}" GO="${probe_bin}" PKGS=./cmd/e2e/... \
    RUN_ARGS='-timeout=75m -v' test-metal 2>/dev/null
)"
if grep -Fqx -- '-run' <<<"${probe_plain}"; then
  fail "test-metal injects -run even when RUN_REGEX is unset"
fi
rm -rf "$(dirname "${probe_bin}")"

expanded="$(
  make -n -C "${repo_root}" GO=/usr/bin/true PKGS=./cmd/e2e/... \
    RUN_ARGS="-timeout=75m -v" test-metal
)"
grep -Fq -- '/usr/bin/true test -tags metal -race -count=1 -timeout=75m -v' <<<"${expanded}" || {
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

# Artifact storage. The harness boots daemons with an explicit environment, so
# a storage configuration the wrapper does not export is simply absent and
# pkg/storage falls back to its local backend at /srv/fc. On an OCI-backed node
# that points the suite at a store imaged never wrote to: vmmd's issue #299
# gate then refuses every cold boot with "scan sidecar missing" for a sidecar
# that exists, in the registry, CRITICAL-clean (observed 2026-09-14).
grep -Fq '/etc/faas/storage.env' "${runner}" ||
  fail "the wrapper does not read the host storage configuration; harness daemons would use the local default"
# `set -a` is what actually makes the sourced values reach the daemons.
grep -Fq 'set -a' "${runner}" ||
  fail "the wrapper sources the storage env without exporting it, so daemons never see it"
# Same '&'-in-an-unquoted-value trap as the DSN: fail loudly rather than
# silently reverting to the local backend.
grep -Fq 'FAAS_STORAGE_BACKEND is empty after sourcing it' "${runner}" ||
  fail "the wrapper silently falls back to the local backend when the host storage env yields an empty one"
# Secrets: the storage env carries registry credentials. Only key NAMES may be
# echoed. A bare `cat` of that file would print the password into the CI log.
if grep -vE '^[[:space:]]*#' "${runner}" | grep -E '(cat|printf .*)[[:space:]]+"?\$\{storage_env_file\}' | grep -vq 'cut -d='; then
  fail "the wrapper prints the storage env file; it contains registry credentials"
fi
# The builder base is a local file only under the local backend; requiring one
# under OCI fails a correctly pre-staged node, where builderd resolves the base
# through the read-through cache instead.
grep -Fq 'FAAS_STORAGE_BACKEND:-local' "${runner}" ||
  fail "the wrapper requires a local builder-base file unconditionally; that is wrong under an OCI backend"

# The NIC lookup must survive a host with no vmmd drop-in directory. A
# DEDICATED acceptance host has none, grep exits 1, and under the runner's own
# `set -Eeuo pipefail` that kills the script before any test runs — observed on
# faas-acceptance-1's first dispatch (34954126133): exit 2, two lines of log.
# Execute the real snippet under the runner's shell options, with the directory
# absent, rather than grepping for the guard.
nic_probe="$(mktemp -d)"
nic_out="$(bash -c '
  set -Eeuo pipefail
  iface=""
  if [[ -z "${iface:-}" ]]; then
    iface="$(
      {
        grep -rhoE "FAAS_PUBLIC_IFACE=[A-Za-z0-9._-]+" "$1/absent.d/" 2>/dev/null || true
      } | head -1 | cut -d= -f2
    )"
  fi
  echo "survived:${iface}"
' _ "${nic_probe}" 2>&1)" || {
  rm -rf "${nic_probe}"
  fail "the NIC lookup dies under set -Eeuo pipefail when the vmmd drop-in directory is absent"
}
rm -rf "${nic_probe}"
[[ "${nic_out}" == "survived:" ]] ||
  fail "the NIC lookup returned ${nic_out} with no drop-in present; expected an empty value it can fall back from"

# Tenant egress NIC. vmmd defaults to eth0; this node has none (ens4), so
# without the override the masquerade rule targets a missing interface and
# every builder microVM boots and then has no egress, dying at guest-init's
# DNS preflight with a zero-byte build log.
grep -Fq 'FAAS_PUBLIC_IFACE' "${runner}" ||
  fail "the wrapper does not resolve the outward NIC; tenant egress NAT would target vmmd's eth0 default"
grep -Fq 'export FAAS_PUBLIC_IFACE' "${runner}" ||
  fail "the wrapper resolves the outward NIC without exporting it, so vmmd never sees it"
# A name that does not exist must be fatal: the NAT rule would install and
# silently do nothing, which is far harder to diagnose than a failed pre-flight.
grep -Fq 'does not exist on this host' "${runner}" ||
  fail "the wrapper does not verify the outward NIC exists"

# Phase mode. The gate now runs the suite as named phases so a failure names a
# layer instead of "the gate is red"; the wrapper must honour FAAS_E2E_PHASE
# and must still refuse an unpartitioned tree.
grep -Fq 'FAAS_E2E_PHASE' "${runner}" ||
  fail "the wrapper does not support phase mode"
grep -Fq 'native_e2e_assert_phase_partition' "${runner}" ||
  fail "the wrapper does not assert the phases partition the metal suite"
# Compiling once and sharing the binaries is what keeps N phases from paying N
# link costs; the Go build cache does not cover the final link.
grep -Fq 'FAAS_E2E_BIN_DIR' "${runner}" ||
  fail "the wrapper does not share compiled daemons across phases"
# Whole-suite contract must NOT be applied per phase: no phase holds all eight
# required tests, so it would fail every phase for tests it never ran.
grep -Fq 'native_e2e_phase_tally' "${runner}" ||
  fail "the wrapper applies the whole-suite verdict to a single phase"

# Orphan reaping. A wedged builder VM outlives the test that owns it and then
# makes the NEXT run's pre-flight refuse the node ("Firecracker workloads are
# active"). Every phase of run 34971983239's predecessor failed in 3s for
# exactly that reason.
grep -Fq 'reap_test_microvms' "${runner}" ||
  fail "the wrapper does not reap microVMs it left running; the next run will be refused"
# Order matters: leakcheck is the assertion that the node is clean. Reaping
# after it would make it permanently unable to fail.
reap_line="$(grep -n 'reap_test_microvms$' "${runner}" | tail -1 | cut -d: -f1)"
leak_line="$(grep -n 'leakcheck.sh' "${runner}" | head -1 | cut -d: -f1)"
[[ -n "${reap_line}" && -n "${leak_line}" && "${reap_line}" -lt "${leak_line}" ]] ||
  fail "reaping must run BEFORE leakcheck, or leakcheck can never fail"

echo "native e2e wrapper contracts OK"
