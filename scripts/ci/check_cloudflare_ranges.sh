#!/usr/bin/env bash
# Compare the pinned Cloudflare ranges with Cloudflare's published lists
# (ADR-20261102081618 §2). The nodes Cloudflare reaches directly admit 80/443
# only from deploy/ansible/roles/nftables/files/cloudflare-ips-v{4,6}.txt, both
# at boot and in vmmd's runtime firewall, so a range Cloudflare adds is refused
# until the pinned files (and the VPC rules gregale-cloudflare-origin-v4/-v6
# on GCP) catch up.
#
#   check_cloudflare_ranges.sh [repo root]
#
# Exits 0 when both families match, 1 on a difference, 2 when a list cannot
# be fetched. CLOUDFLARE_IPS_BASE overrides https://www.cloudflare.com.
set -euo pipefail

root=${1:-$(git rev-parse --show-toplevel)}
base=${CLOUDFLARE_IPS_BASE:-https://www.cloudflare.com}
status=0
for family in v4 v6; do
  pinned="$root/deploy/ansible/roles/nftables/files/cloudflare-ips-$family.txt"
  if ! published=$(curl -fsS --max-time 20 "$base/ips-$family"); then
    echo "cloudflare ranges: cannot fetch $base/ips-$family" >&2
    exit 2
  fi
  if ! difference=$(diff <(grep -v '^[[:space:]]*$' "$pinned" | sort -u) \
    <(printf '%s\n' "$published" | tr -d '\r' | grep -v '^[[:space:]]*$' | sort -u)); then
    echo "cloudflare ranges: $family differs from Cloudflare's list (< pinned only, > published only):"
    printf '%s\n' "$difference"
    status=1
  else
    echo "cloudflare ranges: $family matches ($(grep -c . "$pinned") ranges)"
  fi
done
if ((status)); then
  echo "update the pinned files, release, and update the VPC rules gregale-cloudflare-origin-v4/-v6"
fi
exit "$status"
