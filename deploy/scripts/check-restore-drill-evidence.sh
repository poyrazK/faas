#!/usr/bin/env bash
# check-restore-drill-evidence.sh — CI freshness gate for the M8 acceptance.
#
# The gate is intentionally opt-in at workflow level: CI runs it for a PR
# labelled `m8-done`. Ordinary PRs still run `make lint-drill`, while an M8
# claim must carry a real, committed PASS record from the last 30 days.

set -euo pipefail

DRILLS_DIR="${FAAS_DRILL_RECORD_DIR:-docs/drills}"
MAX_AGE_SECONDS=$((30 * 24 * 60 * 60))
NOW=$(date -u +%s)

required_tokens=(
  "Date (UTC)"
  "Operator"
  "Box"
  "Started"
  "Finished"
  "Wall-clock total"
  "RPO via basebackup"
  "RPO via WAL"
  "Wake latency"
  "Basebackup used"
  "Basebackup SHA-256"
  "Recovery stanza status"
  "host.age SHA-256 (preserved)"
  "Verdict"
  "Operator / commit"
)

fail() {
  echo "m8-evidence-check: $*" >&2
  exit 1
}

timestamp_epoch() {
  local stamp="$1" parsed
  # GNU date is present on the Ubuntu runner; the Python fallback keeps the
  # same gate usable on a macOS operator checkout.
  parsed="$(date -u -d "$stamp" +%s 2>/dev/null || true)"
  if [[ "$parsed" =~ ^[0-9]+$ ]]; then
    printf '%s' "$parsed"
    return 0
  fi
  command -v python3 >/dev/null 2>&1 || return 1
  python3 - "$stamp" <<'PY'
import datetime
import sys

value = datetime.datetime.strptime(sys.argv[1], "%Y-%m-%d %H:%M:%S")
print(int(value.replace(tzinfo=datetime.timezone.utc).timestamp()))
PY
}

[[ -d "$DRILLS_DIR" ]] || fail "${DRILLS_DIR} is missing"

latest=""
latest_epoch=0
shopt -s nullglob
for path in "$DRILLS_DIR"/*-restore-drill.md; do
  base="$(basename "$path")"
  [[ "$base" == TEMPLATE-* ]] && continue
  if [[ "$base" =~ ^([0-9]{4}-[0-9]{2}-[0-9]{2})-([0-9]{6})-restore-drill\.md$ ]]; then
    stamp="${BASH_REMATCH[1]} ${BASH_REMATCH[2]:0:2}:${BASH_REMATCH[2]:2:2}:${BASH_REMATCH[2]:4:2}"
    epoch="$(timestamp_epoch "$stamp" || true)"
    [[ "$epoch" =~ ^[0-9]+$ ]] || fail "invalid UTC timestamp in ${base}"
    (( epoch <= NOW + 300 )) || fail "future-dated evidence ${base}"
    if (( epoch > latest_epoch )); then
      latest="$path"
      latest_epoch="$epoch"
    fi
  fi
done

[[ -n "$latest" ]] || fail "no dated *-restore-drill.md artifact is committed"
(( NOW - latest_epoch <= MAX_AGE_SECONDS )) || fail "latest artifact $(basename "$latest") is older than 30 days"

grep -qE '^\| Verdict \| \*\*PASS\*\* \(bar = 30 min\) \|$' "$latest" \
  || fail "latest artifact does not have a PASS verdict: ${latest}"

for token in "${required_tokens[@]}"; do
  grep -Fq "$token" "$latest" || fail "latest artifact is missing required field: ${token}"
done

# A committed template or a partially filled operator record must never satisfy
# the M8 claim. Check the table values for placeholder markers and empty cells.
while IFS='|' read -r _ field value _; do
  field="${field#"${field%%[![:space:]]*}"}"
  field="${field%"${field##*[![:space:]]}"}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  [[ -n "$field" && "$field" != "Field" && "$field" != "---" ]] || continue
  [[ -n "$value" ]] || fail "empty value for ${field} in ${latest}"
  [[ "$value" != *"<"* && "$value" != *">"* ]] || fail "placeholder value in ${latest}: ${field}"
done < "$latest"

echo "m8-evidence-check: PASS — $(basename "$latest") is a populated PASS record within 30 days"
