#!/usr/bin/env bash
# Pin the production-shape contracts used by the native metal wrapper: the
# shell -> make -> go test argument expansion and the read-only base mountpoints
# guest-init needs before it can attach the writable app layer.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="${repo_root}/scripts/ci/run-native-metal-smoke.sh"

run_args="$(sed -n "s/^[[:space:]]*RUN_ARGS='\([^']*\)' test-metal$/\1/p" "${runner}")"
[[ -n "${run_args}" ]] || {
  echo "could not extract the native metal RUN_ARGS contract" >&2
  exit 1
}

expanded="$(
  make -n -C "${repo_root}" GO=/usr/bin/true PKGS=./pkg/fcvm \
    RUN_ARGS="${run_args}" test-metal
)"
expected='/usr/bin/true test -tags metal -race -count=1 -run=^TestMetalHelloBoot$ -timeout=5m -v ./pkg/fcvm'

grep -Fq -- "${expected}" <<<"${expanded}" || {
  echo "native metal invocation expanded incorrectly:" >&2
  printf '%s\n' "${expanded}" >&2
  exit 1
}

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

echo "native metal wrapper contracts OK"
