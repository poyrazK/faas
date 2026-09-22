#!/usr/bin/env bash
# check_text_encoding.sh — static gate for the two ways free text gets
# corrupted on its way into Postgres.
#
# PostgreSQL rejects three things a Go string does not guard against:
#
#   - invalid UTF-8 in a text column      SQLSTATE 22021
#   - the NUL byte in a text column       "null character not permitted"
#   - non-JSON in a jsonb column          SQLSTATE 22P02
#
# Two idioms in this codebase produced all three, and both are invisible to
# golangci-lint because neither is a type error or an unchecked value.
#
# RULE 1 — JSON built with fmt's %q verb.
#   `fmt.Sprintf(`{"reason":%q}`, s)` looks like JSON encoding and is not.
#   %q is strconv.Quote: for a byte below 0x20 it emits \xNN, an escape JSON
#   has no grammar for. ANSI colour codes (0x1b) in build output and 
#   in a customer-supplied string both reach it. Use safetext.JSONObject over
#   a declared struct.
#
# RULE 2 — free text truncated with a byte slice.
#   `if len(msg) > 1024 { msg = msg[:1024] }` splits a multi-byte rune on any
#   non-ASCII input, and the resulting invalid UTF-8 is rejected by the text
#   column it was being trimmed to fit. Use safetext.Truncate.
#
#   Rule 2 is name-heuristic: the same idiom on a []T slice is correct and
#   common (`out = out[:limit]`), and distinguishing them needs type
#   information this gate does not have. It fires only on identifiers whose
#   names denote text. A slice that trips it can be renamed or allowlisted
#   below; a string that evades it is a gap, not a false negative worth
#   widening the rule for.
#
# Mirrors check_sealed_env_scope.sh's shape: errs counter, stderr-only
# diagnostics, exit 1 on any finding.
set -euo pipefail

root="${1:-.}"
errs=0

# Directories holding first-party Go that can reach Postgres or a notify
# payload. Generated trees and vendored code are out of scope.
scan_dirs=(
  "${root}/pkg"
  "${root}/cmd"
  "${root}/guest"
)

# RULE 1 scope: production code only. Test files build JSON fixtures with %q
# and never write them to a column; flagging those would train reviewers to
# ignore the gate.
rule1_allow_re='pkg/safetext/|_test\.go'

# Identifiers that denote free text for RULE 2, singular forms only. The
# plurals (reasons, logs, details) are almost always []string in this
# codebase, and a slice cap is correct.
text_ident_re='(msg|message|errMsg|errmsg|reason|cause|detail|line|text|body|title|summary|description|output|stderr|stdout|comment|note|value|content|applyErr|lastError)'

# RULE 2 bound: only a fixed cap is dangerous. `s[:i]` where i came from
# strings.Index or a scan is cutting at a boundary the caller already found,
# which is safe by construction — those are the majority of matches and
# flagging them would bury the real ones.
fixed_bound_re='([0-9]+|[a-zA-Z_.]*[Mm]ax[A-Za-z]*)'

rule2_allow_re='pkg/safetext/|_test\.go'

collect_go_files() {
  local dir="$1"
  [[ -d "$dir" ]] || return 0
  find "$dir" -type f -name '*.go' -not -name '*.pb.go' -print
}

# ---------------------------------------------------------------- RULE 1 ---
while IFS= read -r file; do
  [[ "$file" =~ $rule1_allow_re ]] && continue
  # A backtick literal that contains a JSON object opener and a %q verb.
  if matches="$(grep -nE '`[^`]*\{"[^`]*%q' "$file" 2>/dev/null)"; then
    while IFS= read -r m; do
      [[ -z "$m" ]] && continue
      echo "::error file=${file#"${root}/"},line=${m%%:*}::JSON built with fmt %q — %q is strconv.Quote and emits \\xNN escapes that JSON has no grammar for; a jsonb column rejects the result with SQLSTATE 22P02. Use safetext.JSONObject over a declared struct." >&2
      errs=$((errs + 1))
    done <<< "$matches"
  fi
done < <(for d in "${scan_dirs[@]}"; do collect_go_files "$d"; done)

# ---------------------------------------------------------------- RULE 2 ---
while IFS= read -r file; do
  [[ "$file" =~ $rule2_allow_re ]] && continue
  if matches="$(grep -nE "\b${text_ident_re} = [a-zA-Z0-9_.]*\[:${fixed_bound_re}\]" "$file" 2>/dev/null)"; then
    while IFS= read -r m; do
      [[ -z "$m" ]] && continue
      echo "::error file=${file#"${root}/"},line=${m%%:*}::free text truncated with a byte slice — this splits a multi-byte rune and the invalid UTF-8 is rejected by a Postgres text column with SQLSTATE 22021. Use safetext.Truncate (or safetext.TruncateTail to keep the end)." >&2
      errs=$((errs + 1))
    done <<< "$matches"
  fi
done < <(for d in "${scan_dirs[@]}"; do collect_go_files "$d"; done)

if (( errs > 0 )); then
  echo "text-encoding-check: ${errs} violation(s)" >&2
  exit 1
fi
echo "text-encoding-check: clean"
