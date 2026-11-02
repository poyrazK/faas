#!/usr/bin/env bash
# Exercise cloudflare-origin-pull.sh against a fake Cloudflare API
# (ADR-20261102081618 step 2d). ca makes a verifiable pair without any API
# call; without APPLY=1 nothing is written; with it only what is missing is
# written; the token never prints.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PULL="$SCRIPT_DIR/cloudflare-origin-pull.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin" "$ROOT/mock"

# The fake curl logs "METHOD URL DATA" per call. State in $MOCK_DIR: certs
# (a JSON array of uploaded certificates), enabled (present when zone-level
# authenticated origin pulls are on) and ruleset (the entrypoint, or absent).
cat >"$ROOT/bin/curl" <<'MOCK'
#!/usr/bin/env bash
method=GET data=
while (($#)); do
  case "$1" in
    -X) method=$2; shift 2 ;;
    --data) data=$2; shift 2 ;;
    -H) shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
printf '%s %s %s\n' "$method" "$url" "$data" >>"$MOCK_DIR/log"
case "$method $url" in
  "GET "*"/settings/ssl") printf '{"result":{"value":"full"}}' ;;
  "GET "*"/origin_tls_client_auth/settings")
    if [[ -f "$MOCK_DIR/enabled" ]]; then printf '{"result":{"enabled":true}}'; else printf '{"result":{"enabled":false}}'; fi ;;
  "GET "*"/origin_tls_client_auth") jq -c '{result: .}' "$MOCK_DIR/certs" ;;
  "POST "*"/origin_tls_client_auth")
    jq -c --argjson d "$data" '. + [{id: "cert-1", status: "active", expires_on: "2036-01-01", certificate: $d.certificate}]' "$MOCK_DIR/certs" >"$MOCK_DIR/certs.new"
    mv "$MOCK_DIR/certs.new" "$MOCK_DIR/certs"; printf '{"success":true}' ;;
  "PUT "*"/origin_tls_client_auth/settings") touch "$MOCK_DIR/enabled"; printf '{"success":true}' ;;
  "GET "*"/rulesets/phases/http_config_settings/entrypoint")
    if [[ -f "$MOCK_DIR/ruleset" ]]; then jq -c '{result: .}' "$MOCK_DIR/ruleset"; else exit 22; fi ;;
  "PUT "*"/rulesets/phases/http_config_settings/entrypoint")
    jq -c '{id: "rs-1"} + .' <<<"$data" >"$MOCK_DIR/ruleset"; printf '{"success":true}' ;;
  "POST "*"/rulesets/rs-1/rules")
    jq -c --argjson r "$data" '.rules += [$r]' "$MOCK_DIR/ruleset" >"$MOCK_DIR/ruleset.new"
    mv "$MOCK_DIR/ruleset.new" "$MOCK_DIR/ruleset"; printf '{"success":true}' ;;
  *) echo "unexpected call: $method $url" >&2; exit 22 ;;
esac
MOCK
chmod +x "$ROOT/bin/curl"

export PATH="$ROOT/bin:$PATH" MOCK_DIR="$ROOT/mock" CF_ZONE_ID=zone-1 CF_API_TOKEN=secret-token-value APPS_DOMAIN=example.dev
echo '[]' >"$MOCK_DIR/certs"
fail() { echo "cloudflare-origin-pull_test: $*" >&2; exit 1; }
writes() { grep -cE '^(POST|PUT|DELETE) ' "$MOCK_DIR/log" || true; }

out=$(bash "$PULL" ca "$ROOT/pki")
[[ ! -f "$MOCK_DIR/log" ]] || fail "ca called the API"
openssl verify -CAfile "$ROOT/pki/origin-pull-ca.pem" "$ROOT/pki/origin-pull-client.pem" >/dev/null || fail "client certificate does not chain to the CA"
openssl x509 -in "$ROOT/pki/origin-pull-client.pem" -noout -ext extendedKeyUsage | grep -q "Client Authentication" || fail "client certificate lacks clientAuth"
if bash "$PULL" ca "$ROOT/pki" 2>/dev/null; then fail "ca replaced an existing pair"; fi

out=$(bash "$PULL" plan)
grep -q "ssl mode: full" <<<"$out" || fail "plan printed: $out"
grep -q "enabled=false" <<<"$out" || fail "plan printed: $out"
grep -q "rule: absent" <<<"$out" || fail "plan printed: $out"

: >"$MOCK_DIR/log"
out=$(bash "$PULL" upload "$ROOT/pki"; bash "$PULL" strict)
[[ "$(writes)" == 0 ]] || fail "wrote without APPLY=1"
[[ "$(grep -c '^would: ' <<<"$out")" == 3 ]] || fail "dry run printed: $out"

: >"$MOCK_DIR/log"
out=$(APPLY=1 bash "$PULL" upload "$ROOT/pki"; APPLY=1 bash "$PULL" strict)
[[ "$(writes)" == 3 ]] || fail "apply made $(writes) writes, want 3"
grep -q '^POST .*/origin_tls_client_auth .*"private_key"' "$MOCK_DIR/log" || fail "certificate not uploaded with its key"
grep -q '^PUT .*/origin_tls_client_auth/settings {"enabled":true}' "$MOCK_DIR/log" || fail "zone-level setting not turned on"
grep -q '"ssl":"strict"' "$MOCK_DIR/log" || fail "rule does not set Full (strict)"
grep -q 'not http.host in {\\"api.example.dev\\"' "$MOCK_DIR/log" || fail "rule does not exclude the platform hosts"
grep -q "secret-token-value" <<<"$out" && fail "the token printed"

: >"$MOCK_DIR/log"
APPLY=1 bash "$PULL" upload "$ROOT/pki" >/dev/null
APPLY=1 bash "$PULL" strict >/dev/null
[[ "$(writes)" == 0 ]] || fail "a second apply wrote $(writes) times"

# A zone that already has a configuration ruleset gets the rule added to it.
echo '{"id":"rs-1","rules":[{"description":"someone else'"'"'s rule"}]}' >"$MOCK_DIR/ruleset"
: >"$MOCK_DIR/log"
APPLY=1 bash "$PULL" strict >/dev/null
grep -q '^POST .*/rulesets/rs-1/rules' "$MOCK_DIR/log" || fail "rule not added to the existing ruleset"
[[ "$(jq '.rules | length' "$MOCK_DIR/ruleset")" == 2 ]] || fail "the existing rule was not kept"

echo "cloudflare-origin-pull_test: OK"
