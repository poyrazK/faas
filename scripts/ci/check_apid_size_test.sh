#!/usr/bin/env bash
# Hermetic tests for check_apid_size.sh. Every case builds its own package
# directory and baselines, and asserts the exit STATUS, not just the message.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_apid_size.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

failures=0

baseline() {
  local file="$test_root/$1.txt"
  printf '# comment\n%s\n' "$2" > "$file"
  printf '%s\n' "$file"
}

# run <case-name> <want-status> <apid-dir> <baseline-file> [internal-test-baseline-file]
run() {
  local name="$1" want="$2" dir="$3" source_baseline="$4" test_baseline="${5:-$(baseline "generous-$1" 1000)}" got=0 output
  output="$(cd "$repo_root" && APID_DIR="$dir" APID_SIZE_BASELINE_FILE="$source_baseline" \
    APID_INTERNAL_TEST_BASELINE_FILE="$test_baseline" bash "$checker" 2>&1)" || got=$?
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $name — exit $got, want $want" >&2
    printf '%s\n' "$output" | sed 's/^/    /' >&2
    failures=$((failures + 1))
    return
  fi
  echo "ok: $name"
}

# Package with 3 non-test lines, a 4-line internal test, a 5-line external
# test and a 6-line export_test.go bridge. Only the internal test counts
# against the internal-test baseline.
pkg="$test_root/apid"
mkdir -p "$pkg"
printf 'package apid\n\nfunc a() {}\n' > "$pkg/a.go"
printf 'package apid\n\nfunc TestA() {}\nfunc TestB() {}\n' > "$pkg/a_test.go"
printf 'package apid_test\n\nfunc TestC() {}\nfunc TestD() {}\nfunc TestE() {}\n' > "$pkg/c_test.go"
printf 'package apid\n\nvar A = a\nvar B = a\nvar C = a\nvar D = a\n' > "$pkg/export_test.go"

run "equal to baseline passes" 0 "$pkg" "$(baseline equal 3)"
run "under baseline passes" 0 "$pkg" "$(baseline under 10)"
run "growth fails" 1 "$pkg" "$(baseline growth 2)"
run "test files do not count" 0 "$pkg" "$(baseline tests 3)"
run "non-numeric baseline fails" 1 "$pkg" "$(baseline bad abc)"
run "missing baseline fails" 1 "$pkg" "$test_root/nope.txt"
run "missing package fails" 1 "$test_root/nope" "$(baseline nopkg 3)"

run "internal tests equal to baseline pass" 0 "$pkg" "$(baseline src 3)" "$(baseline int-equal 4)"
run "internal test growth fails" 1 "$pkg" "$(baseline src 3)" "$(baseline int-growth 3)"
run "external tests and the bridge do not count" 0 "$pkg" "$(baseline src 3)" "$(baseline int-only 4)"
run "non-numeric internal test baseline fails" 1 "$pkg" "$(baseline src 3)" "$(baseline int-bad abc)"
run "missing internal test baseline fails" 1 "$pkg" "$(baseline src 3)" "$test_root/nope-int.txt"

out="$(APID_DIR="$pkg" APID_SIZE_BASELINE_FILE="$(baseline msg 10)" \
  APID_INTERNAL_TEST_BASELINE_FILE="$(baseline msg-int 10)" bash "$checker" 2>&1)"
if [[ "$out" != *"lower $test_root/msg.txt"* || "$out" != *"lower $test_root/msg-int.txt"* ]]; then
  echo "FAIL: shrink should suggest lowering both baselines" >&2
  printf '%s\n' "$out" | sed 's/^/    /' >&2
  failures=$((failures + 1))
fi

out="$(APID_DIR="$pkg" APID_SIZE_BASELINE_FILE="$(baseline msg-grow 3)" \
  APID_INTERNAL_TEST_BASELINE_FILE="$(baseline msg-int-grow 1)" bash "$checker" 2>&1)" || true
if [[ "$out" != *"package apid_test"* ]]; then
  echo "FAIL: internal test growth should point at the external test package" >&2
  printf '%s\n' "$out" | sed 's/^/    /' >&2
  failures=$((failures + 1))
fi

if (( failures > 0 )); then
  echo "$failures case(s) failed" >&2
  exit 1
fi
echo "all apid-size cases passed"
