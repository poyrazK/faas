#!/usr/bin/env bash
# Verify that an npm tarball contains the payload path that its package
# manifest promises to publish. Keep tar and grep as separate commands:
# piping tar into grep under pipefail lets grep's early success signal SIGPIPE
# as a false failure for valid archives.
set -euo pipefail

if [ "$#" -ne 2 ]; then
	printf 'usage: %s ARCHIVE EXPECTED_PATH\n' "$0" >&2
	exit 2
fi

archive=$1
expected=$2
[ -f "$archive" ] || {
	printf 'error: npm archive does not exist: %s\n' "$archive" >&2
	exit 1
}

listing=$(mktemp "${TMPDIR:-/tmp}/gregale-npm-payload.XXXXXX")
trap 'rm -f "$listing"' EXIT

if ! tar -tzf "$archive" >"$listing"; then
	printf 'error: unable to read npm archive: %s\n' "$archive" >&2
	exit 1
fi

if ! grep -Fqx -- "$expected" "$listing"; then
	printf 'error: npm archive %s does not contain %s\n' "$archive" "$expected" >&2
	cat "$listing" >&2
	exit 1
fi

printf 'ok: %s contains %s\n' "$archive" "$expected"
