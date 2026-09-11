#!/usr/bin/env bash
# Verify that every staged npm package contains the executable payload its
# manifest promises to install. This runs `npm pack` for real so package
# `files` globs are evaluated exactly as they are during publication.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	printf 'usage: %s PUBLISH_ORDER_FILE\n' "$0" >&2
	exit 2
fi

order_file="$1"
if [ ! -f "$order_file" ]; then
	printf 'publish order file not found: %s\n' "$order_file" >&2
	exit 2
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-npm-payloads.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

while IFS= read -r dir; do
	[ -n "$dir" ] || continue
	if [ ! -f "$dir/package.json" ]; then
		printf 'package manifest not found: %s/package.json\n' "$dir" >&2
		exit 1
	fi

	name="$(node -p "require('$dir/package.json').name")"
	if [ "$name" = "gregale" ]; then
		want="package/bin/gregale.js"
	else
		want="package/bin/gregale"
	fi

	tarball="$(cd "$dir" && npm pack --silent </dev/null)"
	archive="$dir/$tarball"
	contents="$tmp_dir/contents.txt"
	# Materialize the complete listing before searching it. Piping tar into
	# grep under pipefail turns grep's successful early exit into tar SIGPIPE
	# and falsely reports a valid package as missing its payload.
	tar -tzf "$archive" >"$contents"
	if ! grep -Fqx "$want" "$contents"; then
		printf '::error::%s would publish without %s\n' "$name" "$want" >&2
		cat "$contents" >&2
		rm -f "$archive"
		exit 1
	fi

	rm -f "$archive"
	printf 'ok: %s contains %s\n' "$name" "${want#package/}"
done <"$order_file"
