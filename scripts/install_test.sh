#!/usr/bin/env bash
# Hermetic tests for scripts/install.sh (ADR-172).
#
# The installer is the highest-consequence script in the repo: it runs as
# `curl | sh` on machines we do not control, and a silent checksum failure
# would install an unverified binary onto someone's PATH. So the network is
# replaced with a stub `curl` serving a fake release tree, and `uname` is
# stubbed so every platform-detection branch is reachable from one host.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/install.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gregale-install-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

STUB="$TMP_DIR/stub-bin"
FAKE_ROOT="$TMP_DIR/fake-release"
TAG="v1.2.3"
SEMVER="1.2.3"

mkdir -p "$STUB" "$FAKE_ROOT/api" "$FAKE_ROOT/dl" "$TMP_DIR/payload"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

# ------------------------------------------------------------------- stubs

# Minimal curl stand-in. Serves $FAKE_ROOT instead of the network and
# mimics `-f` by exiting 22 when the "URL" has no backing file, which is
# what the installer's 404 handling depends on.
cat >"$STUB/curl" <<'EOF'
#!/bin/sh
dest=""
url=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	-o | --output)
		dest="$2"
		shift 2
		;;
	--proto)
		shift 2
		;;
	-*)
		shift
		;;
	*)
		url="$1"
		shift
		;;
	esac
done
case "$url" in
*releases/latest) path="$FAKE_ROOT/api/latest.json" ;;
*releases\?per_page=*) path="$FAKE_ROOT/api/list.json" ;;
*) path="$FAKE_ROOT/dl/$(basename "$url")" ;;
esac
[ -f "$path" ] || exit 22
if [ -n "$dest" ]; then
	cp "$path" "$dest"
else
	cat "$path"
fi
EOF

# uname stand-in so a single host can exercise darwin/linux, amd64/arm64,
# and the rejection paths.
cat >"$STUB/uname" <<'EOF'
#!/bin/sh
case "${1:-}" in
-m) printf '%s\n' "${FAKE_UNAME_M:-x86_64}" ;;
*) printf '%s\n' "${FAKE_UNAME_S:-Linux}" ;;
esac
EOF

chmod 0755 "$STUB/curl" "$STUB/uname"

# wget must not be reachable, or the installer could silently fall through
# to a real network call when the curl stub is misconfigured.
cat >"$STUB/wget" <<'EOF'
#!/bin/sh
echo "wget should not be used when curl is present" >&2
exit 99
EOF
chmod 0755 "$STUB/wget"

# --------------------------------------------------------------- fake release

cat >"$TMP_DIR/payload/gregale" <<'EOF'
#!/bin/sh
printf 'gregale stub %s\n' "$*"
EOF
chmod 0755 "$TMP_DIR/payload/gregale"

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# Archives for all four shipped targets, each holding the stub binary.
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
	tar -czf "$FAKE_ROOT/dl/gregale_${SEMVER}_${target}.tar.gz" \
		-C "$TMP_DIR/payload" gregale
done

write_sums() {
	: >"$FAKE_ROOT/dl/SHA256SUMS"
	for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
		archive="gregale_${SEMVER}_${target}.tar.gz"
		printf '%s  %s\n' "$(sha256_of "$FAKE_ROOT/dl/$archive")" "$archive" \
			>>"$FAKE_ROOT/dl/SHA256SUMS"
	done
}
write_sums

printf '{"tag_name": "%s", "prerelease": false}\n' "$TAG" >"$FAKE_ROOT/api/latest.json"
printf '[{"tag_name": "v2.0.0-rc.7", "prerelease": true}]\n' >"$FAKE_ROOT/api/list.json"

# ------------------------------------------------------------------ harness

# run <expect-ok|expect-fail> <label> [extra install.sh args...]
# Captures combined output into $RUN_OUT for assertions.
RUN_OUT=""
run() {
	local mode="$1" label="$2"
	shift 2
	local status=0
	set +e
	RUN_OUT="$(
		PATH="$STUB:$PATH" \
			HOME="$TMP_DIR/home" \
			FAKE_ROOT="$FAKE_ROOT" \
			FAKE_UNAME_S="${FAKE_UNAME_S:-Linux}" \
			FAKE_UNAME_M="${FAKE_UNAME_M:-x86_64}" \
			GREGALE_API_BASE="https://api.github.com" \
			GREGALE_DOWNLOAD_BASE="https://github.com/poyrazK/faas/releases/download" \
			sh "$SCRIPT" "$@" 2>&1
	)"
	status=$?
	set -e
	case "$mode" in
	expect-ok) [ "$status" -eq 0 ] || fail "$label: expected success, exit $status
$RUN_OUT" ;;
	expect-fail) [ "$status" -ne 0 ] || fail "$label: expected failure, exited 0
$RUN_OUT" ;;
	esac
}

# ---------------------------------------------------------------- happy path

DEST="$TMP_DIR/bin-explicit"
run expect-ok "explicit --version" --version "$TAG" --dir "$DEST"
[ -x "$DEST/gregale" ] || fail "installer did not place an executable binary"
"$DEST/gregale" version >/dev/null || fail "installed binary does not run"
grep -Fq "installed $DEST/gregale" <<<"$RUN_OUT" ||
	fail "installer should report where it installed: $RUN_OUT"

# A bare semver without the leading v must resolve to the same release;
# people copy version numbers out of `gregale version` output.
DEST="$TMP_DIR/bin-bare-semver"
run expect-ok "bare semver --version" --version "$SEMVER" --dir "$DEST"
[ -x "$DEST/gregale" ] || fail "bare semver form did not install"

# ----------------------------------------------------- version resolution

DEST="$TMP_DIR/bin-resolved"
run expect-ok "resolve newest stable" --dir "$DEST"
[ -x "$DEST/gregale" ] || fail "version resolution from the API did not install"

# GitHub's /releases/latest 404s while every release is a prerelease, which
# is this repo's current state. The installer must still work, and must say
# out loud that it served a prerelease.
mv "$FAKE_ROOT/api/latest.json" "$FAKE_ROOT/api/latest.json.off"
cp "$FAKE_ROOT/dl/gregale_${SEMVER}_linux_amd64.tar.gz" \
	"$FAKE_ROOT/dl/gregale_2.0.0-rc.7_linux_amd64.tar.gz"
printf '%s  %s\n' \
	"$(sha256_of "$FAKE_ROOT/dl/gregale_2.0.0-rc.7_linux_amd64.tar.gz")" \
	"gregale_2.0.0-rc.7_linux_amd64.tar.gz" >>"$FAKE_ROOT/dl/SHA256SUMS"

DEST="$TMP_DIR/bin-prerelease"
run expect-ok "prerelease fallback" --dir "$DEST"
[ -x "$DEST/gregale" ] || fail "prerelease fallback did not install"
grep -Fq "no stable release yet" <<<"$RUN_OUT" ||
	fail "prerelease fallback must warn on stderr: $RUN_OUT"
grep -Fq "v2.0.0-rc.7" <<<"$RUN_OUT" ||
	fail "prerelease fallback should name the tag it chose: $RUN_OUT"
mv "$FAKE_ROOT/api/latest.json.off" "$FAKE_ROOT/api/latest.json"

# No releases at all must be a clean error, not a download of "".
mv "$FAKE_ROOT/api/list.json" "$FAKE_ROOT/api/list.json.off"
mv "$FAKE_ROOT/api/latest.json" "$FAKE_ROOT/api/latest.json.off"
run expect-fail "no resolvable release" --dir "$TMP_DIR/bin-none"
grep -Fq "could not resolve a release" <<<"$RUN_OUT" ||
	fail "unresolvable release should say so: $RUN_OUT"
mv "$FAKE_ROOT/api/list.json.off" "$FAKE_ROOT/api/list.json"
mv "$FAKE_ROOT/api/latest.json.off" "$FAKE_ROOT/api/latest.json"

# ------------------------------------------------------ checksum enforcement

# The single most important assertion in this file: a tampered archive must
# not reach PATH.
cp "$FAKE_ROOT/dl/SHA256SUMS" "$TMP_DIR/SHA256SUMS.good"
sed 's/^[0-9a-f]\{64\}/0000000000000000000000000000000000000000000000000000000000000000/' \
	"$TMP_DIR/SHA256SUMS.good" >"$FAKE_ROOT/dl/SHA256SUMS"

DEST="$TMP_DIR/bin-tampered"
run expect-fail "checksum mismatch" --version "$TAG" --dir "$DEST"
grep -Fq "checksum mismatch" <<<"$RUN_OUT" ||
	fail "checksum failure must name itself: $RUN_OUT"
[ ! -e "$DEST/gregale" ] ||
	fail "SECURITY: installer wrote a binary despite a checksum mismatch"

# An archive with no SHA256SUMS line at all is equally unverifiable.
grep -v "linux_amd64" "$TMP_DIR/SHA256SUMS.good" >"$FAKE_ROOT/dl/SHA256SUMS"
DEST="$TMP_DIR/bin-unlisted"
run expect-fail "archive absent from SHA256SUMS" --version "$TAG" --dir "$DEST"
grep -Fq "no entry for" <<<"$RUN_OUT" ||
	fail "missing checksum entry must name itself: $RUN_OUT"
[ ! -e "$DEST/gregale" ] ||
	fail "SECURITY: installer wrote a binary with no checksum entry"

cp "$TMP_DIR/SHA256SUMS.good" "$FAKE_ROOT/dl/SHA256SUMS"

# ---------------------------------------------------- platform detection

# `VAR=x some_function` has unspecified scoping for shell functions, so the
# faked uname values are exported explicitly and reset afterwards.
as_host() {
	FAKE_UNAME_S="$1"
	FAKE_UNAME_M="$2"
}

as_host Darwin arm64
run expect-ok "darwin/arm64" --version "$TAG" --dir "$TMP_DIR/bin-darwin-arm64"
grep -Fq "darwin/arm64" <<<"$RUN_OUT" || fail "expected darwin/arm64 detection: $RUN_OUT"

as_host Linux aarch64
run expect-ok "linux/aarch64 maps to arm64" --version "$TAG" --dir "$TMP_DIR/bin-linux-arm64"
grep -Fq "linux/arm64" <<<"$RUN_OUT" || fail "aarch64 must map to arm64: $RUN_OUT"

as_host Darwin x86_64
run expect-ok "darwin/x86_64 maps to amd64" --version "$TAG" --dir "$TMP_DIR/bin-darwin-amd64"
grep -Fq "darwin/amd64" <<<"$RUN_OUT" || fail "x86_64 must map to amd64: $RUN_OUT"

as_host Linux riscv64
run expect-fail "unsupported arch" --version "$TAG" --dir "$TMP_DIR/bin-riscv"
grep -Fq "unsupported architecture" <<<"$RUN_OUT" ||
	fail "unsupported arch should name itself: $RUN_OUT"

# Windows must fail with a named reason, matching the npm channel's `os`
# gate. cmd/gregale imports pkg/fcvm, which is unix-only.
as_host "MINGW64_NT-10.0" x86_64
run expect-fail "windows rejected" --version "$TAG" --dir "$TMP_DIR/bin-win"
grep -Fq "Windows is not supported" <<<"$RUN_OUT" ||
	fail "windows rejection should name Windows: $RUN_OUT"

as_host SunOS x86_64
run expect-fail "unsupported os" --version "$TAG" --dir "$TMP_DIR/bin-sunos"
grep -Fq "unsupported operating system" <<<"$RUN_OUT" ||
	fail "unsupported OS should name itself: $RUN_OUT"

as_host Linux x86_64

# --------------------------------------------------------- other failures

run expect-fail "nonexistent tag" --version "v9.9.9" --dir "$TMP_DIR/bin-404"
grep -Fq "download failed" <<<"$RUN_OUT" ||
	fail "a 404 archive should report a failed download: $RUN_OUT"

# An archive that unpacks without a gregale binary must not be reported as
# a successful install.
tar -czf "$FAKE_ROOT/dl/gregale_${SEMVER}_linux_amd64.tar.gz" \
	-C "$TMP_DIR" SHA256SUMS.good
write_sums
DEST="$TMP_DIR/bin-empty-archive"
run expect-fail "archive without the binary" --version "$TAG" --dir "$DEST"
grep -Fq "did not contain" <<<"$RUN_OUT" ||
	fail "an archive missing the binary should say so: $RUN_OUT"

run expect-fail "unknown flag" --nope
grep -Fq "unknown flag" <<<"$RUN_OUT" || fail "unknown flags must be rejected: $RUN_OUT"

run expect-fail "flag without a value" --version
grep -Fq "needs a value" <<<"$RUN_OUT" || fail "a dangling flag must be rejected: $RUN_OUT"

run expect-ok "--help" --help
grep -Fq "Install the gregale CLI" <<<"$RUN_OUT" || fail "--help should print usage"

printf 'ok: install.sh\n'
