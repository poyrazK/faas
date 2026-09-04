#!/usr/bin/env bash
# validate-runtime-docs.sh — prevent the operator contract from regressing.
#
# Runtime bases are published OCI artifacts and are staged by imaged. The
# operator docs must not grow a hand-staged ext4/scp recipe again: that path
# bypasses digest verification and is not portable to OVH, Hetzner, or any
# other bare-metal provider.

set -euo pipefail

runtime_docs=(
  docs/runtimes/node24.md
  docs/runtimes/python313.md
  docs/runtimes/go124.md
)

for doc in "${runtime_docs[@]}"; do
  if [[ ! -f "$doc" ]]; then
    echo "::error::runtime contract document is missing: $doc"
    exit 1
  fi

  # These phrases identify the old manual staging instructions. Keep this
  # deny-list intentionally specific so the docs can still explain that
  # manual staging is forbidden.
  if grep -Einq \
    'pre-PR-2|fallback staging|mkfs\.ext4|scp .*\.ext4|hand[- ]copied runtime|stage the .*\.ext4|manual runtime staging' \
    "$doc"; then
    echo "::error::$doc contains a forbidden manual runtime staging instruction"
    exit 1
  fi

  for required in EnsureRuntimeBase digest-pinned; do
    if ! grep -Fq "$required" "$doc"; then
      echo "::error::$doc does not describe $required runtime-base enforcement"
      exit 1
    fi
  done
done

if ! grep -Fq 'FAAS_DEPLOY_BASE_REF_GO124_ALPINE' docs/runtimes/go124.md; then
  echo "::error::go124 runtime docs omit the alpine digest-pinned deployment ref"
  exit 1
fi

echo "Runtime documentation contract passed."
