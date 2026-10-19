#!/usr/bin/env bash
# Exercises .github/actions/deploy/src/cli.sh: the Action finds a bare
# vendored CLI, unpacks the gzip-compressed one release.yml commits,
# checks it against SHA256SUMS, reuses the unpacked copy, and refuses a
# mismatch or a missing CLI.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# shellcheck source=../../.github/actions/deploy/src/cli.sh
source "$root/.github/actions/deploy/src/cli.sh"

fail() { echo "FAIL: $1" >&2; exit 1; }

new_release() {
	local repo="$1"
	mkdir -p "$repo/.github/actions/deploy/bin"
	printf '#!/bin/sh\necho "gregale v-test"\n' > "$tmp/gregale"
	chmod +x "$tmp/gregale"
}

# 1. A bare executable bin/gregale is used as is.
repo="$tmp/plain"; new_release "$repo"
cp "$tmp/gregale" "$repo/.github/actions/deploy/bin/gregale"
got="$(gregale_cli "$repo/.github/actions/deploy")" || fail "bare CLI not found"
[ "$got" = "$repo/.github/actions/deploy/bin/gregale" ] || fail "bare CLI path: $got"

# 2. bin/gregale.gz is unpacked into RUNNER_TEMP and verified.
repo="$tmp/packed"; new_release "$repo"
gzip -9 -n -c "$tmp/gregale" > "$repo/.github/actions/deploy/bin/gregale.gz"
(cd "$tmp" && gregale_cli_sha256 gregale | sed 's/$/  gregale/') > "$repo/SHA256SUMS"
export RUNNER_TEMP="$tmp/runner"
mkdir -p "$RUNNER_TEMP"
got="$(gregale_cli "$repo/.github/actions/deploy")" || fail "packed CLI not resolved"
case "$got" in "$RUNNER_TEMP"/gregale-cli-*/gregale) ;; *) fail "packed CLI path: $got" ;; esac
[ -x "$got" ] || fail "unpacked CLI is not executable"
[ "$("$got")" = "gregale v-test" ] || fail "unpacked CLI does not run"
ls "$(dirname "$got")"/*.tmp >/dev/null 2>&1 && fail "temporary file left behind"

# 3. A second call reuses the unpacked copy instead of unpacking again.
printf '#!/bin/sh\necho reused\n' > "$got"
again="$(gregale_cli "$repo/.github/actions/deploy")" || fail "second resolve failed"
[ "$again" = "$got" ] || fail "second resolve unpacked elsewhere: $again"
[ "$("$again")" = "reused" ] || fail "second resolve rewrote the unpacked CLI"

# 4. An archive that does not match SHA256SUMS is refused with status 2.
repo="$tmp/tampered"; new_release "$repo"
printf '#!/bin/sh\necho tampered\n' | gzip -9 -n -c > "$repo/.github/actions/deploy/bin/gregale.gz"
cp "$tmp/packed/SHA256SUMS" "$repo/SHA256SUMS"
status=0; gregale_cli "$repo/.github/actions/deploy" >/dev/null || status=$?
[ "$status" -eq 2 ] || fail "tampered CLI returned $status, want 2"
ls "$RUNNER_TEMP"/gregale-cli-*/gregale.tmp >/dev/null 2>&1 && fail "tampered temporary file left behind"

# 5. Nothing vendored: status 1.
repo="$tmp/empty"; new_release "$repo"
status=0; gregale_cli "$repo/.github/actions/deploy" >/dev/null || status=$?
[ "$status" -eq 1 ] || fail "missing CLI returned $status, want 1"

echo "deploy Action CLI resolution: PASS"
