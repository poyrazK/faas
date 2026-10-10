#!/usr/bin/env bash
# Combine the two native metal passes into one honest tally.
#
# Both passes run the same pkg/fcvm test binary, so six tests appear in BOTH
# logs: they skip in the package pass (they gate on FAAS_TEST_NETWORK_BATCH)
# and execute in the namespace batch. Summing the per-pass tallies counted
# those six as passed AND skipped simultaneously, which is how run
# 34382870842 reported "435 passed, 16 skipped" when only 10 tests never
# executed. A summary that inflates its own skip count is the same defect
# class as a gate that greps for a string it never asserts.
#
# A test's outcome is its best outcome across both logs: failed anywhere
# wins, then passed anywhere, and skipped only when it never ran in either
# pass. Names, not counts, are the unit — that is what makes the union work.
#
# Usage: metal-tally.sh <package-log> <batch-log>
# Emits: passed=N / skipped=N / failed=N, then one never_ran=<name> per test
# that no pass executed.

set -Eeuo pipefail

[[ "$#" -eq 2 ]] || {
  echo "usage: $0 <package-log> <batch-log>" >&2
  exit 2
}

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# Top-level test names only. `go test -v` indents subtest result lines, so the
# ^ anchor keeps "--- PASS: Parent/child" from inflating every count.
names() {
  local outcome="$1"
  shift
  local log
  for log in "$@"; do
    [[ -f "${log}" ]] || continue
    grep -E "^--- ${outcome}: " "${log}" || true
  done | sed -E "s/^--- ${outcome}: ([^ ]+).*/\1/" | sort -u
}

names FAIL "$1" "$2" >"${work}/failed"
names PASS "$1" "$2" >"${work}/passed.raw"
names SKIP "$1" "$2" >"${work}/skipped.raw"

# A test that failed in one pass stays failed even if the other pass was green.
comm -23 "${work}/passed.raw" "${work}/failed" >"${work}/passed"
sort -u "${work}/passed" "${work}/failed" >"${work}/ran"
comm -23 "${work}/skipped.raw" "${work}/ran" >"${work}/skipped"

count() { grep -c . "$1" || true; }

echo "passed=$(count "${work}/passed")"
echo "skipped=$(count "${work}/skipped")"
echo "failed=$(count "${work}/failed")"
sed 's/^/never_ran=/' "${work}/skipped"
