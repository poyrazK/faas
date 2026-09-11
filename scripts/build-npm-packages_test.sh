#!/usr/bin/env bash
# Hermetic tests for build-npm-packages.sh (ADR-172).
#
# No network, no npm registry, no real gregale binary: the release archives
# are faked with a stub shell script so the staging logic, the rendered
# manifests, and the JS launcher can all be exercised on any machine.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/build-npm-packages.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gregale-npm-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

VERSION="v1.2.3-rc.4"
SEMVER="1.2.3-rc.4"
ARTIFACTS="$TMP_DIR/artifacts"
OUT="$TMP_DIR/staged"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# ------------------------------------------------------------ fake release

mkdir -p "$ARTIFACTS" "$TMP_DIR/stub"
cat >"$TMP_DIR/stub/gregale" <<'EOF'
#!/bin/sh
# Stub stand-in for the compiled CLI.
if [ "${1:-}" = "--boom" ]; then
	exit 7
fi
printf 'stub-gregale %s\n' "$*"
EOF
chmod 0755 "$TMP_DIR/stub/gregale"

for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
	tar -czf "$ARTIFACTS/gregale_${SEMVER}_${target}.tar.gz" \
		-C "$TMP_DIR/stub" gregale
done

# --------------------------------------------------------------- happy path

bash "$SCRIPT" --version "$VERSION" --artifacts-dir "$ARTIFACTS" --out-dir "$OUT" >/dev/null

for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
	dir="$OUT/@gregale/cli-$target"
	[ -d "$dir" ] || fail "platform package not staged: $target"
	[ -x "$dir/bin/gregale" ] || fail "$target binary missing or not executable"
	[ -f "$dir/LICENSE" ] || fail "$target package omits LICENSE"
	grep -Fq "\"name\": \"@gregale/cli-$target\"" "$dir/package.json" ||
		fail "$target package.json has the wrong name"
	grep -Fq "\"version\": \"$SEMVER\"" "$dir/package.json" ||
		fail "$target package.json has the wrong version"
done

# npm's architecture vocabulary differs from Go's; a silent mix-up here
# makes npm install the wrong binary or refuse a valid host.
grep -Fq '"x64"' "$OUT/@gregale/cli-linux-amd64/package.json" ||
	fail "linux-amd64 must declare cpu x64, not amd64"
grep -Fq '"arm64"' "$OUT/@gregale/cli-linux-arm64/package.json" ||
	fail "linux-arm64 must declare cpu arm64"
grep -Fq '"darwin"' "$OUT/@gregale/cli-darwin-arm64/package.json" ||
	fail "darwin-arm64 must declare os darwin"
grep -Fq '"linux"' "$OUT/@gregale/cli-linux-amd64/package.json" ||
	fail "linux-amd64 must declare os linux"

ROOT="$OUT/gregale"
[ -f "$ROOT/package.json" ] || fail "root launcher package not staged"
[ -x "$ROOT/bin/gregale.js" ] || fail "root launcher shim missing"
[ -f "$ROOT/README.md" ] || fail "root package omits README (it is the npm landing page)"
[ -f "$ROOT/LICENSE" ] || fail "root package omits LICENSE"

# Exact pins, not carets: gregale@X must never resolve a binary package
# built from a different commit.
for target in darwin-amd64 darwin-arm64 linux-amd64 linux-arm64; do
	grep -Fq "\"@gregale/cli-$target\": \"$SEMVER\"" "$ROOT/package.json" ||
		fail "root package.json does not pin @gregale/cli-$target to exactly $SEMVER"
done
# `grep && fail` would be wrong here: under `set -e` a non-matching grep
# makes the whole && statement exit 1 and kills the test run. Negative
# assertions use an explicit if.
if grep -Fq '"^' "$ROOT/package.json"; then
	fail "root package.json uses a caret range for a platform package"
fi

# Root must refuse Windows outright rather than install a launcher that
# cannot find a binary (ADR-172).
if grep -Fq '"win32"' "$ROOT/package.json"; then
	fail "root package.json must not list win32 — no Windows build exists"
fi

if grep -rq '__[A-Z_]\{2,\}__' "$OUT"; then
	fail "unsubstituted template placeholder survived into the staged packages"
fi

# Exercise the same npm-pack payload check used by the release workflow. The
# valid case catches pipefail/SIGPIPE regressions; the negative case proves a
# missing executable still fails with a useful package listing.
bash "$REPO_ROOT/scripts/verify-npm-package-payloads.sh" "$OUT/publish-order.txt" >/dev/null ||
	fail "valid staged packages failed the npm payload check"
mv "$OUT/@gregale/cli-linux-amd64/bin/gregale" \
	"$OUT/@gregale/cli-linux-amd64/bin/gregale.missing"
status=0
payload_err="$(bash "$REPO_ROOT/scripts/verify-npm-package-payloads.sh" \
	"$OUT/publish-order.txt" 2>&1)" || status=$?
[ "$status" -ne 0 ] || fail "payload check accepted a package without its executable"
grep -Fq '@gregale/cli-linux-amd64 would publish without package/bin/gregale' <<<"$payload_err" ||
	fail "payload check did not identify the missing executable"
mv "$OUT/@gregale/cli-linux-amd64/bin/gregale.missing" \
	"$OUT/@gregale/cli-linux-amd64/bin/gregale"

# Platform packages must publish before the root package that pins them.
# mapfile is bash 4+; macOS still ships bash 3.2 as /bin/bash, so read the
# file the portable way.
order_count="$(wc -l <"$OUT/publish-order.txt" | tr -d ' ')"
[ "$order_count" -eq 5 ] || fail "publish-order.txt should list 5 packages, got $order_count"
order_last="$(tail -n 1 "$OUT/publish-order.txt")"
[ "$order_last" = "$ROOT" ] ||
	fail "root launcher must be published last, got $order_last"

# ------------------------------------------------------- launcher behaviour

# Exercise the shim for real: lay out the node_modules tree npm would
# produce on this host and run it. This is the one test that proves
# require.resolve finds the binary and exit codes propagate.
if command -v node >/dev/null 2>&1; then
	case "$(uname -s)" in
	Darwin) host_os=darwin ;;
	Linux) host_os=linux ;;
	*) host_os="" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) host_arch=amd64 ;;
	arm64 | aarch64) host_arch=arm64 ;;
	*) host_arch="" ;;
	esac

	if [ -n "$host_os" ] && [ -n "$host_arch" ]; then
		LIVE="$TMP_DIR/live"
		mkdir -p "$LIVE/node_modules/@gregale"
		cp -R "$ROOT/." "$LIVE/"
		cp -R "$OUT/@gregale/cli-$host_os-$host_arch" \
			"$LIVE/node_modules/@gregale/cli-$host_os-$host_arch"

		out="$(cd "$LIVE" && node bin/gregale.js deploy --app demo)" ||
			fail "launcher failed to exec the platform binary"
		[ "$out" = "stub-gregale deploy --app demo" ] ||
			fail "launcher mangled argv: got '$out'"

		status=0
		(cd "$LIVE" && node bin/gregale.js --boom) >/dev/null 2>&1 || status=$?
		[ "$status" -eq 7 ] ||
			fail "launcher must propagate the binary's exit code, got $status"

		# Missing optional dependency is a common npm state (--no-optional,
		# or an unsupported arch). It must produce a named, actionable
		# error, never a raw MODULE_NOT_FOUND stack.
		BARE="$TMP_DIR/bare"
		mkdir -p "$BARE"
		cp -R "$ROOT/." "$BARE/"
		status=0
		err="$(cd "$BARE" && node bin/gregale.js version 2>&1)" || status=$?
		[ "$status" -ne 0 ] || fail "launcher must fail when no platform package is installed"
		grep -Fq "include=optional" <<<"$err" ||
			fail "missing-platform-package error should tell the user how to fix it: $err"
	else
		printf 'skip: launcher exec test (unmapped host %s/%s)\n' "$(uname -s)" "$(uname -m)"
	fi
else
	printf 'skip: launcher exec test (node not on PATH)\n'
fi

# ------------------------------------------------------------ failure modes

expect_fail() {
	local label="$1"
	shift
	if "$@" >/dev/null 2>&1; then
		fail "$label should have failed but exited 0"
	fi
}

expect_fail "a non-semver version" \
	bash "$SCRIPT" --version "not-a-version" --artifacts-dir "$ARTIFACTS" --out-dir "$TMP_DIR/bad-version"
expect_fail "an existing --out-dir" \
	bash "$SCRIPT" --version "$VERSION" --artifacts-dir "$ARTIFACTS" --out-dir "$OUT"

# A missing archive must abort rather than publish a package whose binary
# is absent — npm would happily ship the empty bin/ directory.
PARTIAL="$TMP_DIR/partial-artifacts"
mkdir -p "$PARTIAL"
cp "$ARTIFACTS/gregale_${SEMVER}_linux_amd64.tar.gz" "$PARTIAL/"
expect_fail "a missing platform archive" \
	bash "$SCRIPT" --version "$VERSION" --artifacts-dir "$PARTIAL" --out-dir "$TMP_DIR/partial-out"

expect_fail "a missing --version" \
	bash "$SCRIPT" --artifacts-dir "$ARTIFACTS" --out-dir "$TMP_DIR/no-version"

printf 'ok: build-npm-packages.sh\n'
