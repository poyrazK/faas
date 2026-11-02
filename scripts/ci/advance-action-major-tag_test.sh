#!/usr/bin/env bash
# Hermetic tests for scripts/ci/advance-action-major-tag.sh. A local bare
# repository stands in for origin; each "release" is a commit that vendors
# its tag in .github/actions/deploy/src/version.txt, as release.yml's
# release-branch commit does.
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/advance-action-major-tag.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/advance-action-major-tag-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

# Keep the developer's git config (signing, hooks, default branch) out.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

ORIGIN="$TMP_DIR/origin.git"
WORK="$TMP_DIR/work"
git init --quiet --bare "$ORIGIN"
# GitHub accepts pushes from shallow clones (release.yml pushes from a
# depth-1 checkout, and the script's depth-1 fetch makes this one shallow).
git -C "$ORIGIN" config receive.shallowUpdate true
git init --quiet "$WORK"
git -C "$WORK" remote add origin "file://$ORIGIN"
git -C "$WORK" commit --quiet --allow-empty -m "pre-vendoring source"

# release <tag>: commit a release-branch HEAD vendoring <tag>, print its sha.
release() {
	mkdir -p "$WORK/.github/actions/deploy/src"
	printf '%s\n' "$1" >"$WORK/.github/actions/deploy/src/version.txt"
	git -C "$WORK" add .github/actions/deploy/src/version.txt
	git -C "$WORK" commit --quiet --allow-empty -m "release: bundle gregale $1"
	git -C "$WORK" rev-parse HEAD
}

# advance <tag>: run the script from the release checkout.
OUT=""
advance() {
	OUT="$(cd "$WORK" && RELEASE_TAG="$1" bash "$SCRIPT" 2>&1)" || fail "advance $1 exited non-zero: $OUT"
}

major_tag() { git ls-remote --tags "$ORIGIN" refs/tags/v0 | cut -f1; }

# A prerelease never creates or moves the tag.
release v0.1.18-rc.312 >/dev/null
advance v0.1.18-rc.312
[ -z "$(major_tag)" ] || fail "a prerelease created v0: $OUT"
grep -Fq "is a prerelease" <<<"$OUT" || fail "a prerelease should say why v0 stays: $OUT"

# A tag on a commit that predates vendoring (no version.txt) is replaced.
git -C "$WORK" push --quiet origin "$(git -C "$WORK" rev-list --max-parents=0 HEAD):refs/tags/v0"
stable="$(release v0.1.18)"
advance v0.1.18
[ "$(major_tag)" = "$stable" ] || fail "a stable release over a pre-vendoring v0 must move it: $OUT"

# Later prereleases leave the stable release in place.
release v0.1.19-rc.1 >/dev/null
advance v0.1.19-rc.1
[ "$(major_tag)" = "$stable" ] || fail "a prerelease moved v0 off the stable release: $OUT"

# A re-run of an older stable tag cannot move it backwards.
release v0.1.17 >/dev/null
advance v0.1.17
[ "$(major_tag)" = "$stable" ] || fail "an older stable tag moved v0 backwards: $OUT"
grep -Fq "v0 stays at v0.1.18 (v0.1.17 is not newer)" <<<"$OUT" || fail "an older tag should say why v0 stays: $OUT"

# Neither can a re-run of the same tag.
release v0.1.18 >/dev/null
advance v0.1.18
[ "$(major_tag)" = "$stable" ] || fail "a re-run of the current tag moved v0: $OUT"

# A newer stable release moves it forward, past the prerelease of the same core.
newer="$(release v0.1.19)"
advance v0.1.19
[ "$(major_tag)" = "$newer" ] || fail "a newer stable release must move v0: $OUT"
grep -Fq "v0: v0.1.18 -> v0.1.19" <<<"$OUT" || fail "a move should name both versions: $OUT"

# The first stable release of a project with no v0 tag creates it.
git -C "$ORIGIN" update-ref -d refs/tags/v0
first="$(release v0.2.0)"
advance v0.2.0
[ "$(major_tag)" = "$first" ] || fail "the first stable release must create v0: $OUT"

# Anything but a release tag is refused.
if (cd "$WORK" && RELEASE_TAG=main bash "$SCRIPT" >/dev/null 2>&1); then
	fail "a non-release tag must fail"
fi

printf 'ok: advance-action-major-tag.sh\n'
