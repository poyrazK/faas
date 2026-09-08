#!/usr/bin/env bash
# Pin the shell -> make -> go test argument expansion used by the native metal
# wrapper. A single trailing `$` is consumed by make and concatenates the next
# flag, silently selecting zero tests.

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

echo "native metal invocation contract OK"
