#!/usr/bin/env bash
# Tests for scripts/ci/render-release-notes.sh: the notes must carry the
# real checksums (not the shell text that once reached the release page),
# a verify command that passes with only one platform downloaded, and the
# attestation check for the archives.
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/render-release-notes.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/render-release-notes-test.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

sums="$TMP_DIR/CLI-SHA256SUMS"
printf '%s  gregale\n%s  gregale_1.2.3-rc.4_linux_amd64.tar.gz\n' \
	"$(printf 'a%.0s' {1..64})" "$(printf 'b%.0s' {1..64})" >"$sums"

notes="$(GITHUB_REPOSITORY=poyrazK/faas bash "$SCRIPT" v1.2.3-rc.4 "$sums")"

while IFS= read -r line; do
	grep -Fxq "$line" <<<"$notes" || fail "notes omit checksum line: $line"
done <"$sums"
for want in \
	"## Gregale v1.2.3-rc.4" \
	"curl -fsSL https://get.gregale.dev | sh -s -- --version v1.2.3-rc.4" \
	"sha256sum --ignore-missing -c CLI-SHA256SUMS" \
	"shasum -a 256 --ignore-missing -c CLI-SHA256SUMS" \
	"gh attestation verify gregale_1.2.3-rc.4_linux_amd64.tar.gz -R poyrazK/faas" \
	"npm install -g gregale@1.2.3-rc.4" \
	"- uses: poyrazK/faas/.github/actions/deploy@v0"; do
	grep -Fq -- "$want" <<<"$notes" || fail "notes omit: $want"
done
# shellcheck disable=SC2016 # literal shell text the notes must not contain
for unwanted in '$(cat' 'Compatible server API' 'sha256sum -c CLI-SHA256SUMS'; do
	if grep -Fq -- "$unwanted" <<<"$notes"; then fail "notes still contain: $unwanted"; fi
done
[ "$(grep -c '^```' <<<"$notes")" -eq 14 ] || fail "notes have unbalanced code fences"

# The verify command works with one platform's archive downloaded.
if command -v sha256sum >/dev/null 2>&1; then
	mkdir "$TMP_DIR/dl"
	printf 'archive' >"$TMP_DIR/dl/one.tar.gz"
	(cd "$TMP_DIR/dl" && sha256sum one.tar.gz >CLI-SHA256SUMS &&
		printf '%s  missing.tar.gz\n' "$(printf 'c%.0s' {1..64})" >>CLI-SHA256SUMS &&
		sha256sum --ignore-missing -c CLI-SHA256SUMS >/dev/null) ||
		fail "sha256sum --ignore-missing should pass with only one archive downloaded"
fi

if bash "$SCRIPT" main "$sums" >/dev/null 2>&1; then fail "a non-release tag must fail"; fi
if bash "$SCRIPT" v1.2.3 "$TMP_DIR/absent" >/dev/null 2>&1; then fail "a missing checksum file must fail"; fi

printf 'ok: render-release-notes.sh\n'
