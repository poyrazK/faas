#!/usr/bin/env bash
# Pin the Worker to a release tag's installer (ADR-172).
#
#   deploy/cloudflare/get-gregale-dev/pin.sh v0.1.19
#
# Fetches scripts/install.sh at that tag, computes its sha256, and rewrites
# INSTALLER_REF + INSTALLER_SHA256 in wrangler.toml. Run this, review the
# diff, commit, then `wrangler deploy`.
#
# The two values must move together: the ref decides what is fetched and the
# digest decides whether it is served, so a stale digest fails closed (503)
# rather than serving an unexpected script.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$HERE/wrangler.toml.example"
REPO="${GREGALE_REPO:-poyrazK/faas}"

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

ref="${1:-}"
[ -n "$ref" ] || die "usage: $(basename "$0") <git-tag>   e.g. v0.1.19"

case "$ref" in
main | master)
	die "refusing to pin to a moving branch — this is what 'curl | sh' executes. Pass a tag."
	;;
esac

url="https://raw.githubusercontent.com/${REPO}/${ref}/scripts/install.sh"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/gregale-pin.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp/install.sh" "$url" ||
	die "could not fetch $url (does tag $ref exist?)"

# Guard against pinning a digest for something that is not the installer —
# a 404 page would otherwise hash perfectly happily.
grep -Fq 'one-line installer for the gregale CLI' "$tmp/install.sh" ||
	die "fetched file at $ref does not look like scripts/install.sh"

if command -v sha256sum >/dev/null 2>&1; then
	digest="$(sha256sum "$tmp/install.sh" | cut -d' ' -f1)"
else
	digest="$(shasum -a 256 "$tmp/install.sh" | cut -d' ' -f1)"
fi

# sed -i is not portable between GNU and BSD; rewrite via a temp file.
awk -v ref="$ref" -v digest="$digest" '
	/^INSTALLER_REF[[:space:]]*=/ { print "INSTALLER_REF = \"" ref "\""; next }
	/^INSTALLER_SHA256[[:space:]]*=/ { print "INSTALLER_SHA256 = \"" digest "\""; next }
	{ print }
' "$CONFIG" >"$tmp/wrangler.toml"

grep -Fq "INSTALLER_REF = \"$ref\"" "$tmp/wrangler.toml" ||
	die "failed to rewrite INSTALLER_REF in $CONFIG"
grep -Fq "INSTALLER_SHA256 = \"$digest\"" "$tmp/wrangler.toml" ||
	die "failed to rewrite INSTALLER_SHA256 in $CONFIG"

mv "$tmp/wrangler.toml" "$CONFIG"

printf 'pinned get.gregale.dev to %s\n  sha256 %s\n\nNext: review the diff, commit, then `wrangler deploy`.\n' \
	"$ref" "$digest"
