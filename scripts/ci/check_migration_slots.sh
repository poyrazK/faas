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

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY not set}"
PR_NUMBER="${PR_NUMBER:-}"
BASE_REF="${BASE_REF:-main}"

slots_from_paths() {
	# migrations/00054_account_credits.sql -> 00054
	grep -oE 'migrations/[0-9]{5}_[^/]*\.sql$' | sed -E 's|migrations/([0-9]{5})_.*|\1|' | sort -u
}

git fetch -q origin "${BASE_REF}" || true
base="$(git merge-base HEAD "origin/${BASE_REF}")"

# Slots this PR ADDS. Renames/edits of an existing migration are not a slot
# claim, so filter to added files only.
mine="$(git diff --name-only --diff-filter=A "${base}" HEAD -- 'migrations/*.sql' \
	| slots_from_paths || true)"

if [[ -z "${mine}" ]]; then
	echo "no new migration slots in this PR; nothing to check"
	exit 0
fi
echo "this PR claims slot(s): $(echo "${mine}" | tr '\n' ' ')"

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
