#!/usr/bin/env bash
# Fail when cmd/apid's non-test Go code grows past its committed baseline.
#
# cmd/apid is one package, so every `go test ./cmd/apid` compiles all of it at
# once (~16 GB compiler peak). ADR-977 moves domains into pkg/apid/<domain>; this
# gate keeps new code from refilling package main while that happens.
#
# Growth is allowed only by raising the number in the baseline file in the same
# PR, so reviewers see it. Shrinking passes with a notice: lower the baseline
# in the PR that moved the code so the gain is locked in.
#
# A pure repo-state check: runs identically in CI and locally (`make lint`).

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
  echo "::error::apid-size: must run inside a Git checkout" >&2
  exit 1
}
cd "$repo_root"

apid_dir="${APID_DIR:-cmd/apid}"
baseline_file="${APID_SIZE_BASELINE_FILE:-scripts/ci/apid_size_baseline.txt}"

[[ -d "$apid_dir" ]] || {
  echo "::error::apid-size: missing $apid_dir" >&2
  exit 1
}
[[ -f "$baseline_file" ]] || {
  echo "::error::apid-size: missing $baseline_file" >&2
  exit 1
}

baseline="$(grep -v '^[[:space:]]*#' "$baseline_file" | tr -d '[:space:]')"
[[ "$baseline" =~ ^[0-9]+$ ]] || {
  echo "::error::apid-size: $baseline_file must hold one line count, got '$baseline'" >&2
  exit 1
}

current=0
shopt -s nullglob
for f in "$apid_dir"/*.go; do
  [[ "$f" == *_test.go ]] && continue
  lines=$(wc -l < "$f")
  current=$((current + lines))
done

if (( current > baseline )); then
  cat >&2 <<EOF
::error::apid-size: $apid_dir grew from $baseline to $current non-test lines (+$((current - baseline))).

  New apid code goes in a pkg/apid/<domain> package (ADR-977); cmd/apid
  keeps wiring, the route table and middleware. If this growth is intended,
  raise the number in $baseline_file in this PR.
EOF
  exit 1
fi

if (( current < baseline )); then
  echo "::notice::apid-size: $apid_dir is $current non-test lines, $((baseline - current)) under the baseline; lower $baseline_file to $current to lock in the shrink."
fi
echo "apid-size: $current non-test lines (baseline $baseline)"
