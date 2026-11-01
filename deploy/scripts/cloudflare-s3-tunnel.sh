#!/usr/bin/env bash
# ADR-20261031122757 §2: the cell's s3 tunnel, run from an operator
# workstation (docs/runbooks/S3GatewayComputeLoadTest.md):
#
#   1. create (or reuse) the cell's remotely managed s3 tunnel and set its
#      ingress: each name in INGRESS_HOSTS -> http://127.0.0.1:8084, the
#      compute gateway's data listener; anything else 404;
#   2. stage the connector token on each node in NODES, piped straight from
#      the Cloudflare API into a root-only file over SSH, never printed or
#      written locally, and start faas-cloudflared-s3 where its unit exists;
#   3. create (or update) the proxied record PILOT_HOST -> <tunnel>.cfargotunnel.com.
#
# It never touches s3.<domain>'s record: that keeps reaching the control
# plane until the cutover (ADR step 6). For the one-node load test (step 5)
# NODES is the pilot node alone, so only its connector joins the tunnel.
# Safe to re-run.
#
# Needs CF_ACCOUNT_ID, CF_ZONE_ID, NODES and a Cloudflare API token with
# Account > Cloudflare Tunnel: Edit and Zone > DNS: Edit. Without
# CF_API_TOKEN in the environment the script asks for it without echo.
set -euo pipefail

: "${CF_ACCOUNT_ID:?set CF_ACCOUNT_ID to the Cloudflare account id}"
: "${CF_ZONE_ID:?set CF_ZONE_ID to the zone id of the platform domain}"
: "${NODES:?set NODES to the compute nodes whose connector joins the s3 tunnel (the pilot node for the load test)}"
if [[ -z "${CF_API_TOKEN:-}" ]]; then
  read -r -s -p "Cloudflare API token (Tunnel: Edit, DNS: Edit): " CF_API_TOKEN
  echo
fi

PLATFORM_DOMAIN=${PLATFORM_DOMAIN:-gregale.dev}
TUNNEL_NAME=${TUNNEL_NAME:-gregale-s3-production-us}
PILOT_HOST=${PILOT_HOST:-s3-pilot.$PLATFORM_DOMAIN}
# The cutover adds s3.<domain> here; until then only the pilot host.
INGRESS_HOSTS=${INGRESS_HOSTS:-$PILOT_HOST}
# NODE_SSH runs a command on a node with stdin forwarded: NODE_SSH <node> <command>.
NODE_SSH=${NODE_SSH:-$HOME/gregale-e2e/bin/gpssh}
TOKEN_PATH=/etc/faas/secrets/cloudflared/s3-tunnel-token

read -r -a ingress_hosts <<<"$INGRESS_HOSTS"
for host in "${ingress_hosts[@]}"; do
  if [[ "$host" == "s3.$PLATFORM_DOMAIN" && "${CUTOVER:-}" != yes ]]; then
    echo "refusing to route s3.$PLATFORM_DOMAIN to the tunnel without CUTOVER=yes (ADR step 6)" >&2
    exit 2
  fi
done

api=https://api.cloudflare.com/client/v4
cf() {
  curl -fsS -H "Authorization: Bearer $CF_API_TOKEN" -H 'Content-Type: application/json' "$@"
}

# 1. The tunnel and its ingress.
tunnel_id=$(cf "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel?name=$TUNNEL_NAME&is_deleted=false" | jq -r '.result[0].id // empty')
if [[ -z "$tunnel_id" ]]; then
  tunnel_id=$(cf -X POST "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel" \
    --data "$(jq -cn --arg name "$TUNNEL_NAME" '{name: $name, config_src: "cloudflare"}')" | jq -r .result.id)
  echo "tunnel $TUNNEL_NAME created: $tunnel_id"
else
  echo "tunnel $TUNNEL_NAME exists: $tunnel_id"
fi
cf -X PUT "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations" \
  --data "$(printf '%s\n' "${ingress_hosts[@]}" | jq -Rcn '{config: {ingress: (
      [inputs | {hostname: ., service: "http://127.0.0.1:8084"}] + [{service: "http_status:404"}])}}')" >/dev/null
echo "ingress: ${ingress_hosts[*]} -> http://127.0.0.1:8084, anything else 404"

# 2. The connector token on each named node.
for node in $NODES; do
  cf "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$tunnel_id/token" | jq -j .result |
    "$NODE_SSH" "$node" "sudo install -d -m 0700 $(dirname "$TOKEN_PATH") \
      && sudo install -m 0600 -o root -g root /dev/stdin $TOKEN_PATH \
      && sudo test -s $TOKEN_PATH \
      && if systemctl cat faas-cloudflared-s3.service >/dev/null 2>&1; then \
           sudo systemctl start faas-cloudflared-s3.service && echo \"$node: token staged, connector started\"; \
         else echo \"$node: token staged; the connector starts after a release with object_storage.compute_nodes\"; fi"
done

# 3. The pilot record.
record=$(jq -cn --arg name "$PILOT_HOST" --arg target "$tunnel_id.cfargotunnel.com" \
  '{type: "CNAME", name: $name, content: $target, proxied: true,
    comment: "ADR-20261031122757 s3 tunnel pilot host"}')
existing=$(cf "$api/zones/$CF_ZONE_ID/dns_records?type=CNAME&name=$PILOT_HOST" | jq -r '.result[0].id // empty')
if [[ -n "$existing" ]]; then
  cf -X PUT "$api/zones/$CF_ZONE_ID/dns_records/$existing" --data "$record" >/dev/null
else
  cf -X POST "$api/zones/$CF_ZONE_ID/dns_records" --data "$record" >/dev/null
fi
echo "pilot record: $PILOT_HOST -> $tunnel_id.cfargotunnel.com (proxied)"
echo "next: set faas_object_gateway_public_endpoint=https://$PILOT_HOST on the compute nodes, then the load test runbook"
