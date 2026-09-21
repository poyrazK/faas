#!/usr/bin/env bash
# Fail on a NEWLY duplicated ADR number in docs/adr/.
#
# Concurrent PRs each pick "the next number" and whichever merges second keeps
# it, so main already carries 71 duplicated numbers (ADR-122 is used four
# times). Retro-fixing those would break dozens of `// adr: NNN` citation
# lines, metric help strings and runbooks, so this gate freezes the existing
# set in docs/adr/DUPLICATE_NUMBERS_BASELINE.txt and only stops NEW collisions.
#
# It is a ratchet in both directions: a duplicate that is not in the baseline
# fails, AND a baseline entry that is no longer duplicated fails until the line
# is deleted. Same contract as pkg/state/conformance/uncovered.txt.
#
# Unlike check_spec_cited_tests.sh this needs no PR diff — it is a pure
# repo-state check, so it runs identically in CI and locally (`make lint`).

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$repo_root" ]] || {
  echo "::error::adr-number-uniqueness: must run inside a Git checkout" >&2
  exit 1
}
cd "$repo_root"

adr_dir="${ADR_DIR:-docs/adr}"
baseline_file="${ADR_BASELINE_FILE:-$adr_dir/DUPLICATE_NUMBERS_BASELINE.txt}"

[[ -d "$adr_dir" ]] || {
  echo "::error::adr-number-uniqueness: missing $adr_dir" >&2
  exit 1
}
[[ -f "$baseline_file" ]] || {
  echo "::error::adr-number-uniqueness: missing $baseline_file" >&2
  exit 1
}

# Collect NNN prefixes with a glob rather than `ls | grep` (SC2010): a glob
# cannot be confused by an unusual filename, and needs no pipeline whose
# failure `set -e` would miss.
#
# Files that do not match NNN- (README.md, adr-127-pr-d.md) carry no number to
# collide and are skipped on purpose.
shopt -s nullglob
adr_numbers=""
for path in "$adr_dir"/[0-9][0-9][0-9]-*; do
  name="${path##*/}"
  adr_numbers+="${name:0:3}"$'\n'
done
shopt -u nullglob

adr_numbers="$(printf '%s' "$adr_numbers" | sort)"
[[ -n "$adr_numbers" ]] || {
  echo "::error::adr-number-uniqueness: no ADR files matched NNN- in $adr_dir" >&2
  exit 1
}

actual_dupes="$(printf '%s\n' "$adr_numbers" | uniq -d)"
# `grep -v` exits 1 when a file is all comments or empty, which `set -e` would
# treat as a script failure. An empty baseline is legitimate (it is what a
# fully de-duplicated repo looks like), so tolerate no-match here. The
# fail-closed check on the file's EXISTENCE is above and is the one that
# matters — a missing baseline must never be read as "nothing is baselined".
baseline_dupes="$( { grep -vE '^[[:space:]]*(#|$)' "$baseline_file" || true; } | tr -d '[:blank:]' | sort -u)"

# New collisions: duplicated on disk but absent from the frozen baseline.
new_dupes="$(comm -23 <(printf '%s\n' "$actual_dupes" | sort -u) <(printf '%s\n' "$baseline_dupes"))"

# Stale entries: listed in the baseline but no longer duplicated. The ratchet
# only tightens, so these must be deleted.
stale_entries="$(comm -13 <(printf '%s\n' "$actual_dupes" | sort -u) <(printf '%s\n' "$baseline_dupes"))"

status=0

if [[ -n "$new_dupes" ]]; then
  echo "::error::adr-number-uniqueness: ADR number(s) duplicated by this change" >&2
  while IFS= read -r number; do
    [[ -n "$number" ]] || continue
    echo "  ADR-${number}:" >&2
    shopt -s nullglob
    for path in "$adr_dir/${number}"-*; do
      echo "    ${path##*/}" >&2
    done
    shopt -u nullglob
  done <<< "$new_dupes"
  cat >&2 <<'HINT'

  Pick the next free number. `ls docs/adr/` alone is not enough — also check
  open PR titles, because a PR can claim a number between your check and your
  merge:

    gh pr list --state open --limit 80 --json title \
      --jq '.[] | select(.title|test("ADR-")) | .title'

  Renaming after a rebase? Scope the rename to the references your branch
  introduced. Shared files (api/openapi.yaml, pkg/api/dto.go, pkg/api/limits.go)
  document many ADRs at once, so a blanket sed rewrites other people's.
HINT
  status=1
fi

if [[ -n "$stale_entries" ]]; then
  echo "::error::adr-number-uniqueness: baseline lists number(s) that are no longer duplicated — delete these lines from $baseline_file, the ratchet only tightens" >&2
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    echo "  $entry" >&2
  done <<< "$stale_entries"
  status=1
fi

if ((status != 0)); then
  exit 1
fi

total_files="$(printf '%s\n' "$adr_numbers" | wc -l | tr -d '[:space:]')"
distinct="$(printf '%s\n' "$adr_numbers" | sort -u | wc -l | tr -d '[:space:]')"
baseline_count="$(printf '%s\n' "$baseline_dupes" | grep -c . || true)"
echo "adr-number-uniqueness: OK — ${total_files} ADRs, ${distinct} distinct numbers, ${baseline_count} pre-existing duplicates held at the baseline"
