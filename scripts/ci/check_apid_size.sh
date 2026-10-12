#!/usr/bin/env bash
# Fail when the apid server package grows past its committed baselines:
#
#   1. Non-test Go lines in pkg/apid/apidserver. ADR-982 moves domains into
#      pkg/apid/<domain>; this keeps new code from refilling the server package.
#   2. Internal test lines: _test.go files in `package apidserver` (not
#      apidserver_test), minus the export_test.go bridge. `go test` compiles
#      the package together with these, and test code drives the compiler's
#      memory, so new tests belong in the external apidserver_test package.
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

apid_dir="${APID_DIR:-pkg/apid/apidserver}"
baseline_file="${APID_SIZE_BASELINE_FILE:-scripts/ci/apid_size_baseline.txt}"
test_baseline_file="${APID_INTERNAL_TEST_BASELINE_FILE:-scripts/ci/apid_internal_test_baseline.txt}"

[[ -d "$apid_dir" ]] || {
  echo "::error::apid-size: missing $apid_dir" >&2
  exit 1
}

# read_baseline <file>: print the single line count in a baseline file.
read_baseline() {
  local file="$1" value
  [[ -f "$file" ]] || {
    echo "::error::apid-size: missing $file" >&2
    return 1
  }
  value="$(grep -v '^[[:space:]]*#' "$file" | tr -d '[:space:]')"
  [[ "$value" =~ ^[0-9]+$ ]] || {
    echo "::error::apid-size: $file must hold one line count, got '$value'" >&2
    return 1
  }
  printf '%s\n' "$value"
}

baseline="$(read_baseline "$baseline_file")"
test_baseline="$(read_baseline "$test_baseline_file")"

current=0
internal_tests=0
shopt -s nullglob
for f in "$apid_dir"/*.go; do
  lines=$(wc -l < "$f")
  if [[ "$f" != *_test.go ]]; then
    current=$((current + lines))
    continue
  fi
  [[ "$(basename "$f")" == export_test.go ]] && continue
  pkg="$(grep -m1 -E '^package ' "$f" | awk '{print $2}')"
  [[ "$pkg" == *_test ]] && continue
  internal_tests=$((internal_tests + lines))
done

failed=0
if (( current > baseline )); then
  cat >&2 <<EOF
::error::apid-size: $apid_dir grew from $baseline to $current non-test lines (+$((current - baseline))).

  New apid code goes in a pkg/apid/<domain> package (ADR-982); $apid_dir
  keeps wiring, the route table and middleware. If this growth is intended,
  raise the number in $baseline_file in this PR.
EOF
  failed=1
fi
if (( internal_tests > test_baseline )); then
  cat >&2 <<EOF
::error::apid-size: internal tests in $apid_dir grew from $test_baseline to $internal_tests lines (+$((internal_tests - test_baseline))).

  Write new tests in package $(basename "$apid_dir")_test and reach unexported
  identifiers through export_test.go (ADR-982): internal tests compile with the
  package and grow the larger of its two test compiles. If a test needs what the
  bridge cannot expose, raise the number in $test_baseline_file in this PR.
EOF
  failed=1
fi
(( failed == 0 )) || exit 1

if (( current < baseline )); then
  echo "::notice::apid-size: $apid_dir is $current non-test lines, $((baseline - current)) under the baseline; lower $baseline_file to $current to lock in the shrink."
fi
if (( internal_tests < test_baseline )); then
  echo "::notice::apid-size: internal tests are $internal_tests lines, $((test_baseline - internal_tests)) under the baseline; lower $test_baseline_file to $internal_tests to lock in the shrink."
fi
echo "apid-size: $current non-test lines (baseline $baseline), $internal_tests internal test lines (baseline $test_baseline)"
