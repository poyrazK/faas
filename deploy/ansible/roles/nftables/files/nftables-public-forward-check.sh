#!/usr/bin/env bash
# Load the ADR-1749 public forward guard (pkg/netns/public_forward_policy.go)
# into a throwaway network namespace with the given nft binary, list it, and
# list it as JSON (VMMD parses `nft -j`). Exits non-zero if any step fails.
# Usage: faas-nftables-public-forward-check <nft-binary>
set -euo pipefail
nft="$1"
ns="faas-nftcheck-$$"
rules="$(mktemp)"
trap 'ip netns del "$ns" 2>/dev/null || true; rm -f "$rules"' EXIT
cat >"$rules" <<'NFT'
table inet faas_public_forward {
  chain output {
    type filter hook output priority -1; policy accept;
    meta mark & 0x80000000 == 0x80000000 meta mark set meta mark ^ meta oif
    meta mark & 0x80000000 == 0x80000000 meta mark != 0x80000000 drop
    meta mark == 0x80000000 meta mark set meta mark ^ meta oif
  }
}
NFT
ip netns add "$ns"
ip netns exec "$ns" "$nft" -f "$rules"
ip netns exec "$ns" "$nft" list table inet faas_public_forward >/dev/null
ip netns exec "$ns" "$nft" -j list ruleset >/dev/null
echo "public forward guard loads with $("$nft" --version)"
