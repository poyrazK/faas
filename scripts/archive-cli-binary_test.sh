#!/usr/bin/env bash
# Tests for archive-cli-binary.sh (ADR-172).
#
# The point of this file is the byte-equality assertion below. The release
# pipeline claims its archives are reproducible; that claim shipped as a
# comment and was false, because `tar -czf` stamps the current wall-clock
# time into the gzip header. A comment cannot fail, so the claim is a test.
#
# No Go build: the input is a stub file, which keeps this runnable anywhere.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/archive-cli-binary.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gregale-archive-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

MTIME="@1757000000"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

if ! command -v gtar >/dev/null 2>&1 &&
	! tar --version 2>/dev/null | head -1 | grep -q 'GNU tar'; then
	printf 'skip: archive-cli-binary (GNU tar not available; brew install gnu-tar)\n'
	exit 0
fi

printf '#!/bin/sh\necho stub\n' >"$TMP_DIR/built-cli"
chmod 0755 "$TMP_DIR/built-cli"

# ------------------------------------------------- reproducibility

bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/one.tar.gz" --mtime "$MTIME" >/dev/null
# A full second of wall clock between runs: the original defect was the gzip
# header's MTIME field, which only differs once the clock moves.
sleep 1.2
bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/two.tar.gz" --mtime "$MTIME" >/dev/null

cmp -s "$TMP_DIR/one.tar.gz" "$TMP_DIR/two.tar.gz" ||
	fail "archives are not byte-identical across runs (gzip header timestamp? entry ordering?)"

# Assert the specific field that caused the original bug, so a regression
# gets a precise message instead of just "bytes differ".
header_mtime="$(dd if="$TMP_DIR/one.tar.gz" bs=1 skip=4 count=4 2>/dev/null | od -An -tx1 | tr -d ' \n')"
[ "$header_mtime" = "00000000" ] ||
	fail "gzip header MTIME is $header_mtime, expected 00000000 (is gzip -n still in the pipeline?)"

# ------------------------------------------------- archive contents

entries="$(tar -tzf "$TMP_DIR/one.tar.gz")"
[ "$entries" = "gregale" ] ||
	fail "archive must contain exactly one entry named 'gregale', got: $entries"

# install.sh extracts and execs this; a non-executable entry installs a
# binary nobody can run.
mkdir -p "$TMP_DIR/x"
tar -xzf "$TMP_DIR/one.tar.gz" -C "$TMP_DIR/x"
[ -x "$TMP_DIR/x/gregale" ] || fail "extracted gregale is not executable"

# No builder identity inside the archive.
listing="$(tar -tvzf "$TMP_DIR/one.tar.gz")"
grep -Eq '(^|[[:space:]])0/0([[:space:]]|$)' <<<"$listing" ||
	fail "archive entries should be owned by 0/0, got: $listing"

# The source file's own name must not leak; the entry is always "gregale".
bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/renamed.tar.gz" --mtime "$MTIME" >/dev/null
[ "$(tar -tzf "$TMP_DIR/renamed.tar.gz")" = "gregale" ] ||
	fail "entry name should not follow the input filename"

# A different --mtime must actually change the entry timestamp, otherwise
# the flag is silently ignored and the knob is decorative.
bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/other-mtime.tar.gz" --mtime "@1600000000" >/dev/null
if cmp -s "$TMP_DIR/one.tar.gz" "$TMP_DIR/other-mtime.tar.gz"; then
	fail "--mtime had no effect on the archive bytes"
fi

# ------------------------------------------------- failure modes

expect_fail() {
	local label="$1"
	shift
	if "$@" >/dev/null 2>&1; then
		fail "$label should have failed but exited 0"
	fi
}

expect_fail "a missing --binary" bash "$SCRIPT" --output "$TMP_DIR/x.tar.gz" --mtime "$MTIME"
expect_fail "a missing --output" bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --mtime "$MTIME"
expect_fail "a missing --mtime" bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/x.tar.gz"
expect_fail "a nonexistent binary" bash "$SCRIPT" --binary "$TMP_DIR/nope" --output "$TMP_DIR/x.tar.gz" --mtime "$MTIME"
expect_fail "an unknown flag" bash "$SCRIPT" --binary "$TMP_DIR/built-cli" --output "$TMP_DIR/x.tar.gz" --mtime "$MTIME" --wat

printf 'ok: archive-cli-binary.sh\n'
