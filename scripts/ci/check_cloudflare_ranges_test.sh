#!/usr/bin/env bash
# Exercise check_cloudflare_ranges.sh offline, against published lists served
# from file:// (ADR-20261102081618 §2).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check_cloudflare_ranges.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
fail() { echo "check_cloudflare_ranges_test: $*" >&2; exit 1; }

files="$ROOT/repo/deploy/ansible/roles/nftables/files"
mkdir -p "$files" "$ROOT/published"
printf '173.245.48.0/20\n103.21.244.0/22\n' >"$files/cloudflare-ips-v4.txt"
printf '2400:cb00::/32\n' >"$files/cloudflare-ips-v6.txt"
export CLOUDFLARE_IPS_BASE="file://$ROOT/published"

# Same ranges, other order, CRLF line ends.
printf '103.21.244.0/22\r\n173.245.48.0/20\r\n' >"$ROOT/published/ips-v4"
printf '2400:cb00::/32\n' >"$ROOT/published/ips-v6"
out=$(bash "$CHECK" "$ROOT/repo") || fail "matching lists failed: $out"
grep -q "v4 matches (2 ranges)" <<<"$out" || fail "unexpected output: $out"

# Cloudflare adds a range.
printf '103.21.244.0/22\n173.245.48.0/20\n198.41.128.0/17\n' >"$ROOT/published/ips-v4"
if out=$(bash "$CHECK" "$ROOT/repo"); then fail "a new published range passed"; fi
grep -q "> 198.41.128.0/17" <<<"$out" || fail "the new range is not named: $out"

# A list cannot be fetched.
rm "$ROOT/published/ips-v6"
set +e
bash "$CHECK" "$ROOT/repo" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == 2 ]] || fail "an unreachable list exited $code, want 2"
echo "check_cloudflare_ranges_test: OK"
