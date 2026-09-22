#!/usr/bin/env bash
# Hermetic tests for check_adr_number_uniqueness.sh. No GitHub API, no reads
# of the real docs/adr — every case builds its own ADR directory and baseline.
#
# A gate that has only ever been observed passing is worth nothing: the
# spec-cited gate shipped fail-open precisely because nothing asserted its
# exit code. So every case here asserts the exit STATUS, not just the message.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check_adr_number_uniqueness.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

failures=0

# run <case-name> <want-status> <adr-dir> <baseline-file>
run() {
  local name="$1" want="$2" dir="$3" baseline="$4" got=0 output
  output="$(cd "$repo_root" && ADR_DIR="$dir" ADR_BASELINE_FILE="$baseline" bash "$checker" 2>&1)" || got=$?
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $name — exit $got, want $want" >&2
    printf '%s\n' "$output" | sed 's/^/    /' >&2
    failures=$((failures + 1))
    return
  fi
  echo "ok: $name"
}

new_case() {
  # Split declarations: `local a="$1" b="$test_root/$a"` declares both names
  # (unset) before assigning, so $a is unbound in b's initialiser under `set -u`.
  local name="$1"
  local dir="$test_root/$name"
  mkdir -p "$dir"
  printf '%s\n' "$dir"
}

# --- clean: every number distinct, empty baseline -------------------------
clean="$(new_case clean)"
touch "$clean/001-alpha.md" "$clean/002-beta.md" "$clean/003-gamma.md"
: > "$test_root/empty-baseline.txt"
run "distinct numbers pass with an empty baseline" 0 "$clean" "$test_root/empty-baseline.txt"

# --- a new collision fails ------------------------------------------------
collision="$(new_case collision)"
touch "$collision/001-alpha.md" "$collision/002-beta.md" "$collision/002-beta-again.md"
run "an unbaselined duplicate fails" 1 "$collision" "$test_root/empty-baseline.txt"

# --- a baselined collision passes ----------------------------------------
printf '002\n' > "$test_root/baseline-002.txt"
run "a duplicate held at the baseline passes" 0 "$collision" "$test_root/baseline-002.txt"

# --- comments and blank lines in the baseline are ignored ----------------
printf '# a comment\n\n   \n002\n' > "$test_root/baseline-comments.txt"
run "baseline comments and blank lines are ignored" 0 "$collision" "$test_root/baseline-comments.txt"

# --- a SECOND new collision alongside a baselined one still fails --------
two="$(new_case two)"
touch "$two/002-a.md" "$two/002-b.md" "$two/007-a.md" "$two/007-b.md"
run "a new duplicate fails even when another is baselined" 1 "$two" "$test_root/baseline-002.txt"

# --- stale baseline entry fails (the ratchet tightens) -------------------
run "a baseline entry that is no longer duplicated fails" 1 "$clean" "$test_root/baseline-002.txt"

# --- files without an NNN- prefix are ignored ----------------------------
prefix="$(new_case prefix)"
touch "$prefix/001-alpha.md" "$prefix/README.md" "$prefix/adr-127-pr-d.md"
run "files without a numeric prefix carry no number" 0 "$prefix" "$test_root/empty-baseline.txt"

# --- fail closed on a missing baseline -----------------------------------
run "a missing baseline file fails closed" 1 "$clean" "$test_root/does-not-exist.txt"

# --- fail closed on a directory with no ADRs -----------------------------
empty="$(new_case empty)"
run "an ADR directory with no numbered files fails closed" 1 "$empty" "$test_root/empty-baseline.txt"

# --- fail closed on a missing directory ----------------------------------
run "a missing ADR directory fails closed" 1 "$test_root/absent" "$test_root/empty-baseline.txt"

if ((failures > 0)); then
  echo "check_adr_number_uniqueness_test: $failures case(s) failed" >&2
  exit 1
fi
echo "check_adr_number_uniqueness_test: OK"
