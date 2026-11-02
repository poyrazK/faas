#!/usr/bin/env bash
# Moves the deploy Action's moving major tag (v0 for v0.M.P) to HEAD, the
# release-branch commit that vendors RELEASE_TAG's CLI. release.yml runs it
# after pushing that commit.
#
# Customers deploy with `uses: poyrazK/faas/.github/actions/deploy@v0`, so the
# tag names the newest STABLE release:
#   - a prerelease tag (rc tags ship about twice a day) never moves it;
#   - a stable tag moves it only forward: when RELEASE_TAG takes semver
#     precedence over the release the tag vendors now (its
#     .github/actions/deploy/src/version.txt), so a re-run of an older tag
#     cannot roll every @v0 workflow back. The ordering is the cli-channel
#     pointers' (scripts/ci/cli_channel.py, ADR-20261025092617).
# A tag commit without a readable version.txt predates vendoring and is
# replaced.
#
# Needs RELEASE_TAG. Pushes to the "origin" remote (GIT_REMOTE overrides).
set -euo pipefail

tag="${RELEASE_TAG:?}"
remote="${GIT_REMOTE:-origin}"
order="$(cd "$(dirname "$0")" && pwd)/cli_channel.py"
version_file=.github/actions/deploy/src/version.txt

python3 "$order" newer "$tag" >/dev/null || {
	echo "advance-action-major-tag: $tag is not a release tag" >&2
	exit 1
}
major="${tag%%.*}"

if ! python3 "$order" stable "$tag"; then
	echo "$major stays put: $tag is a prerelease"
	exit 0
fi

current=""
# An unreachable remote fails here; only a missing tag reads as "none".
remote_tag="$(git ls-remote --tags "$remote" "refs/tags/$major")"
if [ -n "$remote_tag" ]; then
	git fetch --quiet --no-tags --depth=1 "$remote" "refs/tags/$major"
	current="$(git show "FETCH_HEAD:$version_file" 2>/dev/null | head -n 1 | tr -d '[:space:]')" || current=""
fi

if ! python3 "$order" newer "$tag" "$current"; then
	echo "$major stays at $current ($tag is not newer)"
	exit 0
fi

git tag -f "$major" HEAD
git push "$remote" "refs/tags/$major" --force
echo "$major: ${current:-none} -> $tag"
