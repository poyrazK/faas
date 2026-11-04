#!/usr/bin/env bash
# Cloudflare's side of direct entry to the compute edges (ADR-20261102081618
# §2, rollout step 2d). Run from an operator workstation, one apps-domain zone
# at a time:
#
#   ca DIR       make the origin-pull CA and the zone's client certificate in
#                DIR (no API call). DIR/origin-pull-ca.pem goes to every
#                compute node as /etc/faas/secrets/edge-tls/origin-pull-ca.pem;
#                keep DIR/origin-pull-ca.key offline or delete it
#   plan         show the zone's SSL mode, its zone-level Authenticated Origin
#                Pulls certificates and setting, and the configuration rule
#                this script manages
#   upload DIR   upload DIR's client certificate as the zone-level
#                Authenticated Origin Pulls certificate, then turn zone-level
#                Authenticated Origin Pulls on. Cloudflare then presents it to
#                every origin in the zone; an origin that does not ask for a
#                client certificate ignores it
#   strict       add a configuration rule setting SSL "Full (strict)" for the
#                app hosts (*.<apps domain> except the platform hosts), so
#                Cloudflare checks the edges' Origin CA certificate. The rest
#                of the zone keeps its SSL mode
#
# Order: ca, copy the CA to the compute nodes, upload, strict, then the
# manifest's public_edge.entry: both. Nothing is written unless APPLY=1;
# without it each change is printed. Needs CF_ZONE_ID and a Cloudflare API
# token with Zone > SSL and Certificates: Edit and Zone > Config Rules: Edit.
# Without CF_API_TOKEN in the environment the script asks for it without echo.
set -euo pipefail

action=${1:-plan}
APPS_DOMAIN=${APPS_DOMAIN:-gregale.dev}
# Hosts under the apps domain that are not apps: they keep the zone's SSL mode.
PLATFORM_HOSTS=${PLATFORM_HOSTS:-api.$APPS_DOMAIN operations.$APPS_DOMAIN s3.$APPS_DOMAIN www.$APPS_DOMAIN get.$APPS_DOMAIN origin.$APPS_DOMAIN app-edge.$APPS_DOMAIN}
RULE_DESCRIPTION="Gregale app hosts: SSL Full (strict) (ADR-20261102081618)"
APPLY=${APPLY:-0}

if [[ "$action" == ca ]]; then
  dir=${2:?usage: $0 ca DIR}
  mkdir -p "$dir"
  for f in origin-pull-ca.pem origin-pull-ca.key origin-pull-client.pem origin-pull-client.key; do
    if [[ -e "$dir/$f" ]]; then
      echo "$dir/$f exists; refusing to replace it" >&2
      exit 1
    fi
  done
  umask 077
  printf 'basicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\n' >"$dir/.ca.ext"
  printf 'basicConstraints=CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=clientAuth\n' >"$dir/.client.ext"
  openssl req -new -newkey rsa:2048 -nodes -keyout "$dir/origin-pull-ca.key" -out "$dir/.ca.csr" \
    -subj "/O=Gregale/CN=Gregale origin pull CA ($APPS_DOMAIN)" 2>/dev/null
  openssl x509 -req -in "$dir/.ca.csr" -signkey "$dir/origin-pull-ca.key" -days 3650 \
    -extfile "$dir/.ca.ext" -out "$dir/origin-pull-ca.pem" 2>/dev/null
  openssl req -new -newkey rsa:2048 -nodes -keyout "$dir/origin-pull-client.key" -out "$dir/.client.csr" \
    -subj "/O=Gregale/CN=Cloudflare origin pull ($APPS_DOMAIN)" 2>/dev/null
  openssl x509 -req -in "$dir/.client.csr" -CA "$dir/origin-pull-ca.pem" -CAkey "$dir/origin-pull-ca.key" \
    -set_serial "0x$(openssl rand -hex 8)" -days 3650 -extfile "$dir/.client.ext" -out "$dir/origin-pull-client.pem" 2>/dev/null
  rm -f "$dir"/.ca.ext "$dir"/.client.ext "$dir"/.ca.csr "$dir"/.client.csr
  chmod 0644 "$dir/origin-pull-ca.pem" "$dir/origin-pull-client.pem"
  openssl verify -CAfile "$dir/origin-pull-ca.pem" "$dir/origin-pull-client.pem" >/dev/null
  echo "made $dir/origin-pull-ca.pem (for the compute nodes) and the client pair (for upload)"
  exit 0
fi

: "${CF_ZONE_ID:?set CF_ZONE_ID to the zone id of the apps domain}"
if [[ -z "${CF_API_TOKEN:-}" ]]; then
  read -r -s -p "Cloudflare API token (SSL and Certificates: Edit, Config Rules: Edit): " CF_API_TOKEN
  echo
fi

api=https://api.cloudflare.com/client/v4
zone=$api/zones/$CF_ZONE_ID
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

# entrypoint prints the zone's http_config_settings ruleset, or {} when the
# zone has none yet (the API answers 404).
entrypoint() {
  cf "$zone/rulesets/phases/http_config_settings/entrypoint" 2>/dev/null | jq -c '.result // {}' || echo '{}'
}

aop_enabled() {
  cf "$zone/origin_tls_client_auth/settings" | jq -r '.result.enabled // false'
}

case "$action" in
  plan)
    echo "ssl mode: $(cf "$zone/settings/ssl" | jq -r '.result.value')"
    echo "zone-level authenticated origin pulls: enabled=$(aop_enabled)"
    cf "$zone/origin_tls_client_auth" |
      jq -r '.result[] | "  certificate \(.id) status=\(.status) expires=\(.expires_on)"'
    present=$(entrypoint | jq --arg d "$RULE_DESCRIPTION" '[.rules[]? | select(.description == $d)] | length')
    echo "app-host Full (strict) rule: $([[ "$present" -gt 0 ]] && echo present || echo absent)"
    ;;
  upload)
    dir=${2:?usage: $0 upload DIR}
    cert=$(cat "$dir/origin-pull-client.pem")
    key=$(cat "$dir/origin-pull-client.key")
    uploaded=$(cf "$zone/origin_tls_client_auth" |
      jq --arg c "$cert" '[.result[] | select((.certificate | gsub("\\s"; "")) == ($c | gsub("\\s"; "")))] | length')
    if [[ "$uploaded" -eq 0 ]]; then
      body=$(jq -cn --arg c "$cert" --arg k "$key" '{certificate: $c, private_key: $k}')
      write "upload the zone-level origin pull certificate" -X POST "$zone/origin_tls_client_auth" --data "$body"
    fi
    if [[ "$(aop_enabled)" != true ]]; then
      write "turn zone-level authenticated origin pulls on" -X PUT "$zone/origin_tls_client_auth/settings" --data '{"enabled":true}'
    fi
    ;;
  strict)
    read -r -a platform <<<"$PLATFORM_HOSTS"
    excluded=$(printf '"%s" ' "${platform[@]}")
    expression="(ends_with(http.host, \".$APPS_DOMAIN\") and not http.host in {${excluded% }})"
    rule=$(jq -cn --arg d "$RULE_DESCRIPTION" --arg e "$expression" \
      '{description: $d, expression: $e, action: "set_config", action_parameters: {ssl: "strict"}, enabled: true}')
    current=$(entrypoint)
    if [[ "$(jq --arg d "$RULE_DESCRIPTION" '[.rules[]? | select(.description == $d)] | length' <<<"$current")" -gt 0 ]]; then
      echo "app-host Full (strict) rule already present"
    elif [[ "$(jq -r '.id // empty' <<<"$current")" == "" ]]; then
      write "create the configuration ruleset with the app-host Full (strict) rule" \
        -X PUT "$zone/rulesets/phases/http_config_settings/entrypoint" --data "$(jq -cn --argjson r "$rule" '{rules: [$r]}')"
    else
      write "add the app-host Full (strict) rule" \
        -X POST "$zone/rulesets/$(jq -r .id <<<"$current")/rules" --data "$rule"
    fi
    ;;
  *)
    echo "usage: $0 ca DIR | plan | upload DIR | strict" >&2
    exit 2
    ;;
esac
