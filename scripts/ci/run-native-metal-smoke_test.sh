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
grep -Fq 'execution_layer_skeleton="${stage_root}/execution-layer-skeleton"' "${runner}" || {
  echo "native metal execution fixture skeleton is not staged" >&2
  exit 1
}
grep -Fq '> "${execution_layer_skeleton}/upper/etc/faas/execution.json"' "${runner}" || {
  echo "native metal execution marker is not staged in the execution layer" >&2
  exit 1
}
grep -Fq '> "${execution_layer_skeleton}/upper/usr/local/bin/python3"' "${runner}" || {
  echo "native metal execution interpreter fixture is not staged" >&2
  exit 1
}
grep -Fq 'export FAAS_TEST_EXECUTION_LAYER_ROOTFS="${execution_layer_path}"' "${runner}" || {
  echo "native metal execution fixture is not exported to the metal tests" >&2
  exit 1
}
if grep -Fq '/etc/faas/uuid.txt' "${runner}"; then
  echo "native metal hello app must not mutate platform-owned /etc/faas" >&2
  exit 1
fi

echo "native metal wrapper contracts OK"
