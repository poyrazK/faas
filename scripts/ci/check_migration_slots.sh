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
		echo "SELF-TEST FAIL: got [${got}], want [${want}]" >&2
		exit 1
	fi
	echo "SELF-TEST PASS: slots_from_paths filters reservations correctly"
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
	echo "no new migration slots in this PR; nothing to check"
	exit 0
fi
echo "this PR claims slot(s): $(echo "${mine}" | tr '\n' ' ')"

reserved="$(printf '%s\n' "${mine_raw}" \
	# grep -E with -o (only print matched portion) avoids the optional-group
	# parens that BSD sed -E rejects on macOS. GNU sed (CI) accepts the
	# nested (.*_)? form, but the gate runs in dev on macOS too and a
	# portable regex is worth the slight readability cost.
	| grep -oE '^migrations/[0-9]{5}_((.*_)?(reservation|reserve_slot))(_[^/]*)?\.sql$' \
	| sed -E 's|migrations/([0-9]{5})_.*|\1|' \
	| sort -u | tr '\n' ' ' || true)"
if [[ -n "${reserved}" ]]; then
	echo "this PR holds reservation slot(s): ${reserved} (excluded from overlap check)"
fi

if ! open_prs="$(gh pr list --repo "${REPO}" --state open --limit 100 \
	--json number --jq '.[].number' 2>/dev/null)"; then
	echo "::warning::could not list open PRs (restricted token or rate limit); skipping cross-PR slot check"
	exit 0
fi

conflict=0
for pr in ${open_prs}; do
	[[ "${pr}" == "${PR_NUMBER}" ]] && continue

	if ! files="$(gh api "repos/${REPO}/pulls/${pr}/files" --paginate \
		--jq '.[] | select(.status=="added") | .filename' 2>/dev/null)"; then
		echo "::warning::could not read files for PR #${pr}; skipping it"
		continue
	fi

	theirs="$(printf '%s\n' "${files}" | slots_from_paths || true)"
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

if (( conflict )); then
	exit 1
fi
echo "no slot collision with any open PR"
