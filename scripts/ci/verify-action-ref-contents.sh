#!/usr/bin/env bash
# verify-action-ref-contents.sh PINNED_SHA MAJOR — check through the GitHub
# Contents API that the deploy Action a release exposes is complete.
#
# PINNED_SHA is the immutable ref the CLI generates: the previous release's
# commit, so it may still vendor the bare bin/gregale (releases before
# rc.275) instead of bin/gregale.gz. MAJOR is the moving tag (v0) this
# release just moved, which must carry this release's compressed CLI.
# Needs GITHUB_REPOSITORY and a gh that can read the repository.
set -euo pipefail

pinned="${1:?pinned Action commit required}"
major="${2:?moving major tag required}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY required}"
action=.github/actions/deploy

has_file() {
	gh api "repos/${repo}/contents/$1?ref=$2" 2>/dev/null | jq -e '.type == "file" and .size > 0' >/dev/null 2>&1
}

require() {
	if ! has_file "$1" "$2"; then
		echo "::error::$1 is missing or empty at $2" >&2
		exit 1
	fi
}

require "$action/action.yml" "$pinned"
if ! has_file "$action/bin/gregale.gz" "$pinned" && ! has_file "$action/bin/gregale" "$pinned"; then
	echo "::error::the pinned Action ref $pinned vendors no gregale CLI (bin/gregale.gz or bin/gregale)" >&2
	exit 1
fi
require "$action/action.yml" "$major"
require "$action/src/cli.sh" "$major"
require "$action/bin/gregale.gz" "$major"
echo "deploy Action contents verified at $pinned and $major"
