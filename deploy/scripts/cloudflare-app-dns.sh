#!/usr/bin/env bash
# DNS chooses each host's origin once the public-timeout Worker is gone
# (ADR-20261030115139, docs/runbooks/AppEdgeCutover.md). Run from an operator
# workstation, one apps-domain zone at a time:
#
#   plan               show the records and Worker routes this script manages
#   platform           give each platform host (the apex, api., operations.,
#                      s3.) that has no record of its own a proxied CNAME to
#                      the control-plane origin. A host with its own record is
#                      never repointed: some are served elsewhere (production's
#                      apex and operations. are on Vercel)
#   pilot HOST         send one app host to the tunnel: a route with no Worker
#                      for HOST/* (it beats the wildcard route) and a proxied
#                      CNAME HOST -> <tunnel>.cfargotunnel.com
#   unpilot HOST       undo pilot: delete HOST's record and its route
#   wildcard           point *.<apps domain> at the tunnel; refuses until every
#                      platform host has its own record
#   unwildcard         point *.<apps domain> back at the control-plane origin
#   keep HOST          keep one host on the control plane after the wildcard
#                      moves: its own proxied CNAME to the origin record
#   drop-worker-routes delete every route that still runs a Worker; until then
#                      a Worker route overrides DNS for the hosts it covers
#
# Nothing is written unless APPLY=1; without it each change is printed. The
# tunnel is read from the app-edge origin record that
# cloudflare-app-edge-tunnel.sh creates. Needs CF_ZONE_ID and a Cloudflare API
# token with Zone > DNS: Edit and Zone > Workers Routes: Edit on that zone.
# Without CF_API_TOKEN in the environment the script asks for it without echo.
set -euo pipefail

: "${CF_ZONE_ID:?set CF_ZONE_ID to the zone id of the apps domain}"
action=${1:-plan}
APPS_DOMAIN=${APPS_DOMAIN:-gregale.dev}
ORIGIN_NAME=${ORIGIN_NAME:-origin.$APPS_DOMAIN}
APP_EDGE_NAME=${APP_EDGE_NAME:-app-edge.$APPS_DOMAIN}
# ADR-1343 §3: apid.IsPlatformHost (the apex, api., operations.) plus s3.
PLATFORM_HOSTS=${PLATFORM_HOSTS:-$APPS_DOMAIN api.$APPS_DOMAIN operations.$APPS_DOMAIN s3.$APPS_DOMAIN}
APPLY=${APPLY:-0}
if [[ -z "${CF_API_TOKEN:-}" ]]; then
  read -r -s -p "Cloudflare API token (DNS: Edit, Workers Routes: Edit): " CF_API_TOKEN
  echo
fi

api=https://api.cloudflare.com/client/v4
cf() {
  curl -fsS -H "Authorization: Bearer $CF_API_TOKEN" -H 'Content-Type: application/json' "$@"
}

# write prints a change, and makes it when APPLY=1.
write() {
  local what=$1
  shift
  if [[ "$APPLY" == 1 ]]; then
    cf "$@" >/dev/null
    echo "done: $what"
  else
    echo "would: $what"
  fi
}

# records_of NAME prints "id type content proxied" for each of NAME's A, AAAA
# and CNAME records; record_of prints the first.
records_of() {
  cf "$api/zones/$CF_ZONE_ID/dns_records?name=$(jq -rn --arg n "$1" '$n | @uri')" |
    jq -r '.result[] | select(.type == "A" or .type == "AAAA" or .type == "CNAME")
      | "\(.id) \(.type) \(.content) \(.proxied)"'
}
record_of() { records_of "$1" | head -n 1; }

# own_record NAME prints NAME's first address record as "type content", or
# fails when it has none (so it would follow the wildcard).
own_record() {
  local record
  record=$(record_of "$1")
  [[ -n "$record" ]] || return 1
  cut -d' ' -f2,3 <<<"$record"
}

# point NAME TARGET makes NAME a proxied CNAME to TARGET. A name with more
# than one address record is left for the operator.
point() {
  local name=$1 target=$2 id type content proxied body
  if (($(records_of "$name" | wc -l) > 1)); then
    echo "$name has several address records; fix it by hand" >&2
    exit 1
  fi
  read -r id type content proxied < <(record_of "$name") || true
  if [[ "${type:-}" == CNAME && "${content:-}" == "$target" && "${proxied:-}" == true ]]; then
    echo "ok: $name -> $target"
    return
  fi
  body=$(jq -cn --arg name "$name" --arg target "$target" \
    '{type: "CNAME", name: $name, content: $target, proxied: true,
      comment: "ADR-20261030115139: DNS chooses the origin"}')
  if [[ -n "${id:-}" ]]; then
    write "$name: $type ${content:-} -> CNAME $target (proxied)" -X PUT "$api/zones/$CF_ZONE_ID/dns_records/$id" --data "$body"
  else
    write "$name: new CNAME $target (proxied)" -X POST "$api/zones/$CF_ZONE_ID/dns_records" --data "$body"
  fi
}

tunnel_target() {
  local id type content proxied
  read -r id type content proxied < <(record_of "$APP_EDGE_NAME") || true
  if [[ "${content:-}" != *.cfargotunnel.com ]]; then
    echo "$APP_EDGE_NAME does not point at a tunnel; run cloudflare-app-edge-tunnel.sh first" >&2
    exit 1
  fi
  echo "$content"
}

routes() {
  cf "$api/zones/$CF_ZONE_ID/workers/routes" | jq -r '.result[] | "\(.id) \(.pattern) \(.script // "-")"'
}

case "$action" in
  plan)
    for name in $PLATFORM_HOSTS "*.$APPS_DOMAIN" "$ORIGIN_NAME" "$APP_EDGE_NAME"; do
      printf '%-32s %s\n' "$name" "$(record_of "$name" | cut -d' ' -f2- || true)"
    done
    # A name that is not an app and has no record of its own follows the
    # wildcard to the tunnel, where the edge answers 421.
    echo "every address record (name type content proxied):"
    cf "$api/zones/$CF_ZONE_ID/dns_records?per_page=1000" |
      jq -r '.result[] | select(.type == "A" or .type == "AAAA" or .type == "CNAME")
        | "  \(.name) \(.type) \(.content) \(.proxied)"'
    echo "worker routes (id pattern worker):"
    routes | sed 's/^/  /'
    ;;
  platform)
    for name in $PLATFORM_HOSTS; do
      if record=$(own_record "$name"); then
        echo "ok: $name has its own record ($record), left alone"
      else
        point "$name" "$ORIGIN_NAME"
      fi
    done
    ;;
  pilot)
    host=${2:?usage: pilot HOST}
    if ! routes | awk -v p="$host/*" '$2 == p { found = 1 } END { exit !found }'; then
      write "route $host/* with no Worker" -X POST "$api/zones/$CF_ZONE_ID/workers/routes" \
        --data "$(jq -cn --arg p "$host/*" '{pattern: $p}')"
    else
      echo "ok: route $host/* exists"
    fi
    point "$host" "$(tunnel_target)"
    ;;
  unpilot)
    host=${2:?usage: unpilot HOST}
    read -r id _ < <(record_of "$host") || true
    if [[ -n "${id:-}" ]]; then
      write "delete record $host" -X DELETE "$api/zones/$CF_ZONE_ID/dns_records/$id"
    fi
    while read -r route_id _; do
      write "delete route $host/*" -X DELETE "$api/zones/$CF_ZONE_ID/workers/routes/$route_id"
    done < <(routes | awk -v p="$host/*" '$2 == p')
    ;;
  wildcard)
    for name in $PLATFORM_HOSTS; do
      if ! own_record "$name" >/dev/null; then
        echo "$name has no record of its own and would follow the wildcard; run '$0 platform' first" >&2
        exit 1
      fi
    done
    point "*.$APPS_DOMAIN" "$(tunnel_target)"
    ;;
  unwildcard)
    point "*.$APPS_DOMAIN" "$ORIGIN_NAME"
    ;;
  keep)
    point "${2:?usage: keep HOST}" "$ORIGIN_NAME"
    ;;
  drop-worker-routes)
    while read -r route_id pattern script; do
      [[ "$script" == - ]] && continue
      write "delete route $pattern (Worker $script)" -X DELETE "$api/zones/$CF_ZONE_ID/workers/routes/$route_id"
    done < <(routes)
    ;;
  *)
    echo "unknown action $action (plan, platform, pilot HOST, unpilot HOST, wildcard, unwildcard, keep HOST, drop-worker-routes)" >&2
    exit 2
    ;;
esac
[[ "$APPLY" == 1 || "$action" == plan ]] || echo "dry run: set APPLY=1 to make these changes"
