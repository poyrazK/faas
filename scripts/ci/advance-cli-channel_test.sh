#!/usr/bin/env bash
# Hermetic tests for scripts/ci/advance-cli-channel.sh (ADR-20261025092617).
# A stub gh keeps the cli-channel release and its assets in a directory.
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/advance-cli-channel.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/advance-cli-channel-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
STATE="$TMP_DIR/release"
export STATE TMP_DIR
mkdir -p "$TMP_DIR/bin"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

cat >"$TMP_DIR/bin/gh" <<'EOF'
#!/bin/sh
# gh release <view|create|download|upload> <name> ... against $STATE.
[ "$1" = release ] || exit 64
cmd="$2"
shift 2
case "$cmd" in
view) [ -d "$STATE" ] ;;
create)
	printf '%s\n' "$*" >"$TMP_DIR/create.args"
	mkdir -p "$STATE"
	;;
download)
	pattern=""
	while [ "$#" -gt 0 ]; do
		case "$1" in --pattern) pattern="$2"; shift 2 ;; *) shift ;; esac
	done
	[ -f "$STATE/$pattern" ] || exit 1
	cat "$STATE/$pattern"
	;;
upload)
	[ -d "$STATE" ] || exit 1
	cp "$2" "$STATE/$(basename "$2")"
	;;
*) exit 64 ;;
esac
EOF
chmod 0755 "$TMP_DIR/bin/gh"

advance() { # <tag>
	PATH="$TMP_DIR/bin:$PATH" GITHUB_REPOSITORY=poyrazK/faas RELEASE_TAG="$1" bash "$SCRIPT" >/dev/null
}
pointer() { cat "$STATE/$1" 2>/dev/null || true; }

# The first prerelease creates the channel as a prerelease that is never
# GitHub's "latest", and writes CLI-LATEST only.
advance v0.1.18-rc.292
grep -Fq -- "--prerelease" "$TMP_DIR/create.args" || fail "channel must be a prerelease"
grep -Fq -- "--latest=false" "$TMP_DIR/create.args" || fail "channel must never be latest"
[ "$(pointer CLI-LATEST)" = v0.1.18-rc.292 ] || fail "CLI-LATEST not written"
[ -z "$(pointer CLI-STABLE)" ] || fail "a prerelease must not write CLI-STABLE"

# Pointers move only forward: a re-run of an older tag changes nothing.
advance v0.1.18-rc.291
[ "$(pointer CLI-LATEST)" = v0.1.18-rc.292 ] || fail "an older tag moved CLI-LATEST back"

# A stable tag moves both pointers; a later prerelease moves only CLI-LATEST.
advance v0.1.18
[ "$(pointer CLI-LATEST)" = v0.1.18 ] || fail "stable tag did not move CLI-LATEST"
[ "$(pointer CLI-STABLE)" = v0.1.18 ] || fail "stable tag did not write CLI-STABLE"
advance v0.1.19-rc.1
[ "$(pointer CLI-LATEST)" = v0.1.19-rc.1 ] || fail "newer prerelease did not move CLI-LATEST"
[ "$(pointer CLI-STABLE)" = v0.1.18 ] || fail "a prerelease moved CLI-STABLE"

# A tag that is not a release tag is refused before anything is written.
if PATH="$TMP_DIR/bin:$PATH" GITHUB_REPOSITORY=poyrazK/faas RELEASE_TAG="cli-channel" bash "$SCRIPT" >/dev/null 2>&1; then
	fail "a non-release tag must fail"
fi
[ "$(pointer CLI-LATEST)" = v0.1.19-rc.1 ] || fail "a refused tag changed CLI-LATEST"

printf 'ok: advance-cli-channel.sh\n'
