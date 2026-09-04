#!/usr/bin/env bash
# check_migration_slots.sh — fail a PR that claims a migration slot another
# OPEN pull request has already claimed.
#
# Why this exists
# ---------------
# migrations/embed_test.go::TestMigrationsUniquePrefixes already rejects
# duplicate slots — but only once both files sit on the same branch. A PR is
# built against refs/pull/N/merge, which is that PR merged into main AS IT IS
# NOW. It cannot see a sibling PR's files. So two PRs can each add 00054_*.sql,
# both go green, and the collision only exists after the second one merges:
#
#   panic: goose: duplicate version 54 detected
#
# At that point main is broken, every other open PR goes red, and the deploy
# fails (issue #366 — PRs #337 and #346 both landed on 00054 on 2026-07-27;
# the same shape hit slots 51 and 52 before that).
#
# The post-merge gate is the wrong place to find this: by the time it fires
# the damage is on main. This script moves the check to PR time, which is the
# only point where renumbering is cheap.
#
# Behaviour
# ---------
# Collision  -> exit 1 with the conflicting PR number and the slot.
# No overlap -> exit 0.
# API failure (fork PR with a restricted token, rate limit) -> warn, exit 0.
# The gate is advisory infrastructure, not a security control; the post-merge
# unique-prefix test remains the backstop.
set -euo pipefail

# Migration filename -> 5-digit slot prefix, with reservation carve-out.
# Reservation files (NNNNN_reserve_slot.sql, NNNNN_no_op_slot_reservation.sql)
# do NOT claim a slot — drop them before extracting the prefix so the
# overlap check below never sees them. Real schemas at the same slot
# still collide normally. ADR-041.
slots_from_paths() {
	grep -v -E '^migrations/[0-9]{5}_(.*_)?(reservation|reserve_slot)(_[^/]*)?\.sql$' \
		| grep -oE 'migrations/[0-9]{5}_[^/]*\.sql$' \
		| sed -E 's|migrations/([0-9]{5})_.*|\1|' \
		| sort -u \
		|| true
}

# all_slots_from_paths includes reservations. It is used to tell the local
# contiguity test which gaps are already claimed by sibling PRs; reservations
# are not executable schema claims, but they still explain a planned gap.
all_slots_from_paths() {
	grep -oE '^migrations/[0-9]{5}_[^/]*\.sql$' \
		| sed -E 's|migrations/([0-9]{5})_.*|\1|' \
		| sort -u \
		|| true
}

emit_external_slots() {
	if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
		printf 'external_slots=%s\n' "${1:-}" >>"${GITHUB_OUTPUT}"
	fi
}

# Inverse of slots_from_paths: emit ONLY the reservation slots. Used to log
# the carved-out slots for reviewer visibility (so a maintainer reading CI
# output knows what was skipped, not just what collided). Lives in its own
# function rather than as an inline `$(...)` substitution because bash 3.2
# rejects the `\<newline># comment\<newline>| pipe` continuation pattern
# that PR #392 first used here (`syntax error near unexpected token |'`).
reserved_from_paths() {
	# grep -oE (not sed -nE) keeps the regex portable across BSD/GNU; the
	# optional-group parens `(.*_)?` are rejected by BSD sed -E.
	grep -oE '^migrations/[0-9]{5}_((.*_)?(reservation|reserve_slot))(_[^/]*)?\.sql$' \
		| sed -E 's|migrations/([0-9]{5})_.*|\1|' \
		| sort -u \
		|| true
}

# --- self-test (BATS_TEST=1) ---------------------------------------------
# Verifies slots_from_paths' reservation carve-out in isolation. No gh /
# git dependency, no env-var requirement, <1 s. Wired into CI via the
# migration slot gate self-test step so drift between this regex and the
# ADR is caught at PR time, not when a misclassified filename silently
# lets two real schemas through. Calls the SAME slots_from_paths function
# used by the production gate above so a regex typo fails the self-test.
if [[ "${BATS_TEST:-0}" == "1" ]]; then
	fixtures=(
		'migrations/00054_account_credits.sql'          # real schema, must surface
		'migrations/00055_placeholder_reserve_slot.sql' # reservation at a UNIQUE slot; if the regex regresses, this surfaces 00055 and the want string fails to match
		'migrations/00056_reserve_slot.sql'             # reservation, must be hidden
		'migrations/00056_no_op_slot_reservation.sql'   # alt spelling, must be hidden
		'migrations/00057_sessions.sql'                 # real schema, must surface
		'migrations/00056_reserved_credits.sql'         # NOT a reservation, must surface
	)
	got="$(printf '%s\n' "${fixtures[@]}" | slots_from_paths | sort | tr '\n' ',' | sed 's/,$//')"
	want="00054,00056,00057"
	if [[ "${got}" != "${want}" ]]; then
		echo "SELF-TEST FAIL: slots_from_paths got [${got}], want [${want}]" >&2
		exit 1
	fi
	got_reserved="$(printf '%s\n' "${fixtures[@]}" | reserved_from_paths | sort | tr '\n' ',' | sed 's/,$//')"
	want_reserved="00055,00056"
	if [[ "${got_reserved}" != "${want_reserved}" ]]; then
		echo "SELF-TEST FAIL: reserved_from_paths got [${got_reserved}], want [${want_reserved}]" >&2
		exit 1
	fi
	got_all="$(printf '%s\n' "${fixtures[@]}" | all_slots_from_paths | sort | tr '\n' ',' | sed 's/,$//')"
	want_all="00054,00055,00056,00057"
	if [[ "${got_all}" != "${want_all}" ]]; then
		echo "SELF-TEST FAIL: all_slots_from_paths got [${got_all}], want [${want_all}]" >&2
		exit 1
	fi
	echo "SELF-TEST PASS: slots_from_paths and reserved_from_paths filter reservations correctly"
	exit 0
fi

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY not set}"
PR_NUMBER="${PR_NUMBER:-}"
BASE_REF="${BASE_REF:-main}"

git fetch -q origin "${BASE_REF}" || true
base="$(git merge-base HEAD "origin/${BASE_REF}")"

# Slots this PR ADDS. Renames/edits of an existing migration are not a slot
# claim, so filter to added files only.
mine_raw="$(git diff --name-only --diff-filter=A "${base}" HEAD -- 'migrations/*.sql' || true)"
mine="$(printf '%s\n' "${mine_raw}" | slots_from_paths)"

if [[ -z "${mine}" ]]; then
	# Keep walking open PRs below so the caller can still receive the
	# externally claimed gaps. A migration-free PR can be tested against
	# a merge ref that contains a real migration from main followed by
	# slots owned by sibling PRs; exiting here would leave
	# FAAS_MIGRATION_ALLOWED_GAPS empty and make that unrelated PR fail
	# TestMigrationsContiguous.
	echo "no new migration slots in this PR; checking sibling claims for contiguity"
else
	echo "this PR claims slot(s): $(echo "${mine}" | tr '\n' ' ')"
fi

reserved="$(printf '%s\n' "${mine_raw}" | reserved_from_paths | tr '\n' ' ' || true)"
if [[ -n "${reserved}" ]]; then
	echo "this PR holds reservation slot(s): ${reserved} (excluded from overlap check)"
fi

if ! open_prs="$(gh pr list --repo "${REPO}" --state open --limit 100 \
	--json number --jq '.[].number' 2>/dev/null)"; then
	emit_external_slots ""
	echo "::warning::could not list open PRs (restricted token or rate limit); skipping cross-PR slot check"
	exit 0
fi

conflict=0
sibling_slots=""
for pr in ${open_prs}; do
	[[ "${pr}" == "${PR_NUMBER}" ]] && continue

	if ! files="$(gh api "repos/${REPO}/pulls/${pr}/files" --paginate \
		--jq '.[] | select(.status=="added") | .filename' 2>/dev/null)"; then
		echo "::warning::could not read files for PR #${pr}; skipping it"
		continue
	fi

	theirs="$(printf '%s\n' "${files}" | slots_from_paths || true)"
	theirs_all="$(printf '%s\n' "${files}" | all_slots_from_paths || true)"
	if [[ -n "${theirs_all}" ]]; then
		sibling_slots+="${theirs_all}"$'\n'
	fi
	[[ -z "${theirs}" ]] && continue

	# comm needs sorted input; both sides are sorted -u already.
	overlap="$(comm -12 <(printf '%s\n' "${mine}") <(printf '%s\n' "${theirs}") || true)"
	if [[ -n "${overlap}" ]]; then
		for slot in ${overlap}; do
			# 10# forces base-10 so 00054 -> 54 rather than being read as
			# octal; goose reports the un-padded number in its panic.
			echo "::error::migration slot ${slot} is also claimed by open PR #${pr} (https://github.com/${REPO}/pull/${pr}). Whichever merges second will panic goose with \"duplicate version $((10#${slot}))\" and break main. Renumber this PR's migration to the next free slot before merging."
		done
		conflict=1
	fi
done

external_slots="$(printf '%s' "${sibling_slots}" | sort -u | paste -sd, -)"
emit_external_slots "${external_slots}"

if (( conflict )); then
	exit 1
fi
echo "no slot collision with any open PR"
