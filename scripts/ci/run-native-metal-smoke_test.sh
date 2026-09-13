#!/usr/bin/env bash
# Pin the production-shape contracts used by the native metal wrapper: the
# shell -> make -> go test argument expansion, the read-only base mountpoints
# guest-init needs, and the production-shaped writable app layer.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${repo_root}/scripts/ci/run-native-metal-smoke.sh"

run_args="$(sed -n "s/^[[:space:]]*RUN_ARGS='\([^']*\)' test-metal.*$/\1/p" "${runner}")"
[[ -n "${run_args}" ]] || {
  echo "could not extract the native metal RUN_ARGS contract" >&2
  exit 1
}

expanded="$(
  make -n -C "${repo_root}" GO=/usr/bin/true PKGS=./pkg/fcvm \
    RUN_ARGS="${run_args}" test-metal
)"
expected='/usr/bin/true test -tags metal -race -count=1 -timeout=30m -v ./pkg/fcvm'

grep -Fq -- "${expected}" <<<"${expanded}" || {
  echo "native metal invocation expanded incorrectly:" >&2
  printf '%s\n' "${expanded}" >&2
  exit 1
}

# The whole point of the gate: no -run filter. This job used to execute one
# test out of the 142 metal-tagged tests in pkg/fcvm, and a -run added "just
# to triage a flake" is how it would silently go back to one.
if grep -q -- '-run=' <<<"${run_args}"; then
  echo "native metal RUN_ARGS carries a -run filter (${run_args}); the gate must run the whole pkg/fcvm metal package" >&2
  exit 1
fi

# A green check must mean tests executed. Zero-executed has to be a failure,
# which is exactly how the old self-hosted job looked dormant rather than
# broken for 100 consecutive dispatches.
grep -Fq 'native metal smoke: no metal test executed' "${runner}" || {
  echo "native metal wrapper does not fail when zero tests execute" >&2
  exit 1
}
grep -Fq 'passed, ${skipped} skipped, ${failed} failed' "${runner}" || {
  echo "native metal wrapper does not report the passed/skipped/failed tally" >&2
  exit 1
}

# The namespace batch is a second pass, not an argument tweak, so it needs
# its own pins: the six tests that manipulate /run/netns skipped entirely
# until it existed.
grep -Fq 'unshare --mount --net --propagation private' "${runner}" || {
  echo "native metal wrapper no longer runs the namespace batch under unshare" >&2
  exit 1
}
grep -Fq 'mount -t tmpfs tmpfs /run/netns' "${runner}" || {
  echo "native metal wrapper does not give the namespace batch a private /run/netns" >&2
  exit 1
}
# Must be an ASSIGNMENT on a non-comment line. The first version of this
# pin grepped for the bare name and matched the comment above the command,
# so it passed while the six tests skipped for want of the variable. A
# check that asserts a string appears somewhere is not a check.
grep -vE '^[[:space:]]*#' "${runner}" | grep -Fq 'FAAS_TEST_NETWORK_BATCH=1' || {
  echo "native metal wrapper does not set FAAS_TEST_NETWORK_BATCH=1 for the batch (a comment mentioning it does not count)" >&2
  exit 1
}
grep -Fq 'namespace batch executed no test' "${runner}" || {
  echo "native metal wrapper does not fail when the namespace batch runs nothing" >&2
  exit 1
}
for batch_test in TestMetalImageBindMount TestMetalIPSetupBatch TestMetalFreshNetworkPolicy \
  TestMetalReusedLeaseNeighbor TestMetalPreparedBridgeMAC TestMetalPreparedNetworkOwnership; do
  grep -Fq "${batch_test}" "${runner}" || {
    echo "native metal wrapper dropped ${batch_test} from the namespace batch" >&2
    exit 1
  }
done

# The combined tally is arithmetic, not a string, so it gets driven with real
# logs instead of grepped for. Run 34382870842 reported "435 passed, 16
# skipped" while only 10 tests never executed: the six batch tests were summed
# as skipped in the package pass AND passed in the batch. A per-pass sum
# cannot express that, so the runner must not go back to one.
tally="${repo_root}/scripts/ci/metal-tally.sh"
[[ -x "${tally}" ]] || {
  echo "scripts/ci/metal-tally.sh is missing or not executable" >&2
  exit 1
}
if grep -Fq 'skipped + batch_skipped' "${runner}"; then
  echo "native metal wrapper sums per-pass skip counts again; a test that skips in one pass and runs in the other would be double-counted" >&2
  exit 1
fi

tally_work="$(mktemp -d)"
trap 'rm -rf "${tally_work}"' EXIT

assert_tally() { # label, expected-passed, expected-skipped, expected-failed
  local label="$1" want_p="$2" want_s="$3" want_f="$4"
  local out got_p got_s got_f
  out="$("${tally}" "${tally_work}/pkg.log" "${tally_work}/batch.log")"
  got_p="$(sed -n 's/^passed=//p' <<<"${out}")"
  got_s="$(sed -n 's/^skipped=//p' <<<"${out}")"
  got_f="$(sed -n 's/^failed=//p' <<<"${out}")"
  if [[ "${got_p}" != "${want_p}" || "${got_s}" != "${want_s}" || "${got_f}" != "${want_f}" ]]; then
    echo "metal tally ${label}: want ${want_p}/${want_s}/${want_f} passed/skipped/failed, got ${got_p}/${got_s}/${got_f}" >&2
    printf '%s\n' "${out}" >&2
    exit 1
  fi
}

# The shape that was misreported: one test skips in the package pass and runs
# in the batch, one skips in both and is the only real gap.
cat >"${tally_work}/pkg.log" <<'LOG'
--- PASS: TestMetalHelloBoot (1.00s)
--- PASS: TestMetalWakeLatency (2.00s)
--- SKIP: TestMetalIPSetupBatch (0.00s)
--- SKIP: TestMetalBuilderAcceptance (0.00s)
LOG
cat >"${tally_work}/batch.log" <<'LOG'
--- PASS: TestMetalIPSetupBatch (4.84s)
LOG
assert_tally "deferred-to-batch" 3 1 0

# Skipped in both passes is still one gap, not two.
cat >"${tally_work}/batch.log" <<'LOG'
--- SKIP: TestMetalIPSetupBatch (0.00s)
LOG
assert_tally "skipped-in-both" 2 2 0

# Failing anywhere outranks skipping, and outranks passing in the other pass.
cat >"${tally_work}/batch.log" <<'LOG'
--- FAIL: TestMetalIPSetupBatch (4.84s)
--- FAIL: TestMetalHelloBoot (1.00s)
LOG
assert_tally "failed-outranks" 1 1 2

# Subtest result lines are indented; counting them would inflate every number.
cat >"${tally_work}/pkg.log" <<'LOG'
--- PASS: TestMetalHelloBoot (1.00s)
    --- PASS: TestMetalHelloBoot/restore (0.50s)
    --- SKIP: TestMetalHelloBoot/stub (0.00s)
--- SKIP: TestMetalBuilderAcceptance (0.00s)
LOG
: >"${tally_work}/batch.log"
assert_tally "subtests-ignored" 1 1 0

# An absent batch log must degrade to the package pass, not crash the summary.
rm -f "${tally_work}/batch.log"
assert_tally "missing-batch-log" 1 1 0
cat >"${tally_work}/batch.log" <<'LOG'
LOG

base_mountpoints="$(sed -n 's/^base_mountpoints=(\(.*\))$/\1/p' "${runner}")"
[[ -n "${base_mountpoints}" ]] || {
  echo "could not extract the native metal base mountpoint contract" >&2
  exit 1
}
for mountpoint in dev overlay proc run sys sys/fs/cgroup tmp; do
  [[ " ${base_mountpoints} " == *" ${mountpoint} "* ]] || {
    echo "native metal base fixture is missing /${mountpoint}" >&2
    exit 1
  }
done
grep -Fq "for mountpoint in \"\${base_mountpoints[@]}\"; do" "${runner}" || {
  echo "native metal base mountpoint contract is not applied" >&2
  exit 1
}
grep -Fq "mkdir -p \"\${base_skeleton}/\${mountpoint}\"" "${runner}" || {
  echo "native metal base mountpoints are not created in the fixture" >&2
  exit 1
}

grep -Fq "\"\${layer_skeleton}/upper/etc/faas\" \"\${layer_skeleton}/upper/tmp\"" "${runner}" || {
  echo "native metal app fixture is not staged under the overlay upper directory" >&2
  exit 1
}
grep -Fq "> \"\${layer_skeleton}/upper/etc/faas/app.json\"" "${runner}" || {
  echo "native metal app manifest is not staged in the writable app layer" >&2
  exit 1
}
if grep -Fq '/etc/faas/uuid.txt' "${runner}"; then
  echo "native metal hello app must not mutate platform-owned /etc/faas" >&2
  exit 1
fi

echo "native metal wrapper contracts OK"
