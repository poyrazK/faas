#!/usr/bin/env bash
# Hermetic regression test for the npm payload verifier (issue #1909).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
checker="$repo_root/scripts/ci/check-npm-payload.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/gregale-npm-payload-test.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/package/bin" "$tmp_dir/package"
printf '#!/bin/sh\n' >"$tmp_dir/package/bin/gregale"
printf '{"name":"gregale"}\n' >"$tmp_dir/package/package.json"

valid="$tmp_dir/valid.tgz"
tar -czf "$valid" -C "$tmp_dir" package/bin/gregale package/package.json

# The verifier runs with pipefail enabled and must accept a valid payload.
bash "$checker" "$valid" package/bin/gregale >/dev/null

missing="$tmp_dir/missing.tgz"
tar -czf "$missing" -C "$tmp_dir" package/package.json
if output="$(bash "$checker" "$missing" package/bin/gregale 2>&1)"; then
	printf 'FAIL: missing payload was accepted\n' >&2
	exit 1
fi
grep -Fq 'does not contain package/bin/gregale' <<<"$output" || {
	printf 'FAIL: missing-payload error omitted the expected path\n%s\n' "$output" >&2
	exit 1
}
grep -Fq 'package/package.json' <<<"$output" || {
	printf 'FAIL: missing-payload error omitted the archive listing\n%s\n' "$output" >&2
	exit 1
}

printf 'ok: npm payload verifier\n'
