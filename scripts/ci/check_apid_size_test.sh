#!/usr/bin/env bash
# Hermetic tests for check_apid_size.sh. Every case builds its own package
# directory and baseline, and asserts the exit STATUS, not just the message.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_apid_size.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

failures=0

# run <case-name> <want-status> <apid-dir> <baseline-file>
run() {
  local name="$1" want="$2" dir="$3" baseline="$4" got=0 output
  output="$(cd "$repo_root" && APID_DIR="$dir" APID_SIZE_BASELINE_FILE="$baseline" bash "$checker" 2>&1)" || got=$?
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $name — exit $got, want $want" >&2
    printf '%s\n' "$output" | sed 's/^/    /' >&2
    failures=$((failures + 1))
    return
  fi
  echo "ok: $name"
}

baseline() {
  local file="$test_root/$1.txt"
  printf '# comment\n%s\n' "$2" > "$file"
  printf '%s\n' "$file"
}

# Package with 3 non-test lines and a test file that must not count.
pkg="$test_root/apid"
mkdir -p "$pkg"
printf 'package main\n\nfunc a() {}\n' > "$pkg/a.go"
printf 'package main\n\nfunc TestA() {}\nfunc TestB() {}\n' > "$pkg/a_test.go"

run "equal to baseline passes" 0 "$pkg" "$(baseline equal 3)"
run "under baseline passes" 0 "$pkg" "$(baseline under 10)"
run "growth fails" 1 "$pkg" "$(baseline growth 2)"
run "test files do not count" 0 "$pkg" "$(baseline tests 3)"
run "non-numeric baseline fails" 1 "$pkg" "$(baseline bad abc)"
run "missing baseline fails" 1 "$pkg" "$test_root/nope.txt"
run "missing package fails" 1 "$test_root/nope" "$(baseline nopkg 3)"

if [[ "$(APID_DIR="$pkg" APID_SIZE_BASELINE_FILE="$(baseline msg 10)" bash "$checker" 2>&1)" != *"lower"* ]]; then
  echo "FAIL: shrink should suggest lowering the baseline" >&2
  failures=$((failures + 1))
fi

if (( failures > 0 )); then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "all apid-size cases passed"
