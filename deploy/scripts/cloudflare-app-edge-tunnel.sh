#!/usr/bin/env bash
# ADR-1343 cutover, docs/runbooks/AppEdgeCutover.md steps 1–3, run from an
# operator workstation:
#
#   1. create (or reuse) the cell's remotely managed app tunnel and set its
#      ingress: *.<apps domain> -> http://127.0.0.1:8090, anything else 404;
#   2. stage the connector token on every compute node, piped straight from
#      the Cloudflare API into a root-only file over SSH, never printed or
#      written locally, and start the connector where its unit exists;
#   3. create (or update) the proxied origin record the public-timeout Worker
#      resolves pilot hosts to: app-edge.<apps domain> -> <tunnel>.cfargotunnel.com.
#
# No app traffic moves: nothing routes to the tunnel until the Worker lists a
# host in APP_EDGE_HOSTS (runbook step 4). Safe to re-run.
#
# Needs CF_ACCOUNT_ID, CF_ZONE_ID and a Cloudflare API token with
# Account > Cloudflare Tunnel: Edit and Zone > DNS: Edit. Without
# CF_API_TOKEN in the environment the script asks for it without echo.
set -euo pipefail

: "${CF_ACCOUNT_ID:?set CF_ACCOUNT_ID to the Cloudflare account id}"
: "${CF_ZONE_ID:?set CF_ZONE_ID to the zone id of the apps domain}"
if [[ -z "${CF_API_TOKEN:-}" ]]; then
  read -r -s -p "Cloudflare API token (Tunnel: Edit, DNS: Edit): " CF_API_TOKEN
  echo
fi

APPS_DOMAIN=${APPS_DOMAIN:-gregale.dev}
# ADR-20261026100000: alias apps domains (space-separated) reach the same app
# edge; gatewayd-public rewrites their hosts to the apps domain. Each alias
# zone keeps its own Worker route and origin record.
APPS_DOMAIN_ALIASES=${APPS_DOMAIN_ALIASES:-}
TUNNEL_NAME=${TUNNEL_NAME:-gregale-apps-production-us}
ORIGIN_NAME=${ORIGIN_NAME:-app-edge.$APPS_DOMAIN}
NODES=${NODES:-gregale-compute-1 gregale-compute-2}
# NODE_SSH runs a command on a node with stdin forwarded: NODE_SSH <node> <command>.
NODE_SSH=${NODE_SSH:-$HOME/gregale-e2e/bin/gpssh}
TOKEN_PATH=/etc/faas/secrets/cloudflared/tunnel-token

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
hosts=("*.$APPS_DOMAIN")
for alias in $APPS_DOMAIN_ALIASES; do
  hosts+=("*.$alias")
done
cf -X PUT "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$tunnel_id/configurations" \
  --data "$(printf '%s\n' "${hosts[@]}" | jq -Rcn '{config: {ingress: (
      [inputs | {hostname: ., service: "http://127.0.0.1:8090"}] + [{service: "http_status:404"}])}}')" >/dev/null
echo "ingress: ${hosts[*]} -> http://127.0.0.1:8090, anything else 404"

# 2. The connector token on every compute node.
for node in $NODES; do
  cf "$api/accounts/$CF_ACCOUNT_ID/cfd_tunnel/$tunnel_id/token" | jq -j .result |
    "$NODE_SSH" "$node" "sudo install -d -m 0700 $(dirname "$TOKEN_PATH") \
      && sudo install -m 0600 -o root -g root /dev/stdin $TOKEN_PATH \
      && sudo test -s $TOKEN_PATH \
      && if systemctl cat faas-cloudflared.service >/dev/null 2>&1; then \
           sudo systemctl start faas-cloudflared.service && echo \"$node: token staged, connector started\"; \
         else echo \"$node: token staged; the connector starts after a release with public_edge.compute_nodes\"; fi"
done

# 3. The origin record the Worker resolves pilot hosts to.
record=$(jq -cn --arg name "$ORIGIN_NAME" --arg target "$tunnel_id.cfargotunnel.com" \
  '{type: "CNAME", name: $name, content: $target, proxied: true,
    comment: "ADR-1343 app edge origin for the public-timeout Worker"}')
existing=$(cf "$api/zones/$CF_ZONE_ID/dns_records?type=CNAME&name=$ORIGIN_NAME" | jq -r '.result[0].id // empty')
if [[ -n "$existing" ]]; then
  cf -X PUT "$api/zones/$CF_ZONE_ID/dns_records/$existing" --data "$record" >/dev/null
else
  cf -X POST "$api/zones/$CF_ZONE_ID/dns_records" --data "$record" >/dev/null
fi
echo "origin record: $ORIGIN_NAME -> $tunnel_id.cfargotunnel.com (proxied)"
echo "next: make check-compute-edges CP_SSH=<user@control-plane>, then runbook step 4"
