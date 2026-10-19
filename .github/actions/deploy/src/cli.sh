#!/usr/bin/env bash
# src/cli.sh — locate the gregale CLI vendored into this Action (ADR-093).
#
# Sourced by src/run.sh and by ../environment-preflight/src/run.sh.
#
# release.yml vendors the linux-amd64 CLI gzip-compressed as
# bin/gregale.gz: GitHub refuses any file over 100 MB, and the bare binary
# passed that in rc.274. gregale_cli unpacks it once per job into
# RUNNER_TEMP, keyed by the archive's digest, and checks the result against
# the release's SHA256SUMS (the repository root of the release commit). A
# bare bin/gregale, as older releases and local tests vendor it, is used
# as is.

# gregale_cli DEPLOY_ACTION_PATH — print the path of an executable gregale
# CLI. Returns 1 when none is vendored, 2 when the unpacked CLI does not
# match SHA256SUMS.
gregale_cli() {
	local action="$1"
	local plain="$action/bin/gregale" packed="$action/bin/gregale.gz"
	local sums="$action/../../../SHA256SUMS"
	if [ -x "$plain" ]; then
		printf '%s\n' "$plain"
		return 0
	fi
	[ -f "$packed" ] || return 1

	local key dir out
	key="$(gregale_cli_sha256 "$packed")" || return 1
	dir="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gregale-cli-${key:0:16}"
	out="$dir/gregale"
	if [ -x "$out" ]; then
		printf '%s\n' "$out"
		return 0
	fi
	mkdir -p "$dir" || return 1
	gzip -dc "$packed" >"$out.tmp" || { rm -f "$out.tmp"; return 1; }
	if [ -f "$sums" ]; then
		local want got
		want="$(awk '$2 == "gregale" || $2 == "*gregale" { print $1; exit }' "$sums")"
		if [ -n "$want" ]; then
			got="$(gregale_cli_sha256 "$out.tmp")" || { rm -f "$out.tmp"; return 1; }
			if [ "$got" != "$want" ]; then
				rm -f "$out.tmp"
				return 2
			fi
		fi
	fi
	chmod 0755 "$out.tmp" && mv -f "$out.tmp" "$out" || return 1
	printf '%s\n' "$out"
}

gregale_cli_sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}
