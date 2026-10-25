#!/usr/bin/env bash
# Exercise cloudflare-app-edge-tunnel.sh against a fake Cloudflare API and a
# fake node SSH (ADR-1343 cutover). The connector token must reach every node
# and never appear in the script's output.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SETUP="$SCRIPT_DIR/cloudflare-app-edge-tunnel.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# The fake curl logs "METHOD URL DATA" per call and answers from MOCK_* state.
cat >"$ROOT/bin/curl" <<'EOF'
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
printf '%s %s %s\n' "$method" "$url" "$data" >>"$MOCK_LOG"
case "$method $url" in
  "GET "*"/cfd_tunnel?name="*)
    if [[ -n "${MOCK_TUNNEL:-}" ]]; then printf '{"result":[{"id":"%s"}]}' "$MOCK_TUNNEL"; else printf '{"result":[]}'; fi ;;
  "POST "*"/cfd_tunnel") printf '{"result":{"id":"tun-new"}}' ;;
  "PUT "*"/configurations") printf '{"success":true}' ;;
  "GET "*"/token") printf '{"result":"SECRET-CONNECTOR-TOKEN"}' ;;
  "GET "*"/dns_records?"*)
    if [[ -n "${MOCK_RECORD:-}" ]]; then printf '{"result":[{"id":"%s"}]}' "$MOCK_RECORD"; else printf '{"result":[]}'; fi ;;
  "POST "*"/dns_records"|"PUT "*"/dns_records/"*) printf '{"success":true}' ;;
  *) echo "unexpected call: $method $url" >&2; exit 22 ;;
esac
EOF
# The fake node SSH stores what it receives on stdin per node.
cat >"$ROOT/bin/node-ssh" <<'EOF'
#!/usr/bin/env bash
cat >"$MOCK_STAGED/$1"
echo "$1: token staged"
EOF
chmod +x "$ROOT/bin/curl" "$ROOT/bin/node-ssh"

run() {
  rm -rf "$ROOT/staged" && mkdir -p "$ROOT/staged"
  : >"$ROOT/log"
  PATH="$ROOT/bin:$PATH" MOCK_LOG="$ROOT/log" MOCK_STAGED="$ROOT/staged" \
    CF_ACCOUNT_ID=acct CF_ZONE_ID=zone CF_API_TOKEN=api-token \
    NODE_SSH="$ROOT/bin/node-ssh" NODES="cn-1 cn-2" "$@" bash "$SETUP"
}
fail() { echo "FAIL: $*" >&2; exit 1; }

# A fresh cell: the tunnel and the record are created.
out="$(run env)"
[[ "$out" != *SECRET-CONNECTOR-TOKEN* ]] || fail "the token was printed: $out"
for node in cn-1 cn-2; do
  [[ "$(cat "$ROOT/staged/$node")" == SECRET-CONNECTOR-TOKEN ]] || fail "$node got no token"
done
grep -q '^POST .*/accounts/acct/cfd_tunnel {"name":"gregale-apps-production-us","config_src":"cloudflare"}' "$ROOT/log" \
  || fail "tunnel not created as remotely managed: $(cat "$ROOT/log")"
grep -q '^PUT .*/cfd_tunnel/tun-new/configurations .*"hostname":"\*.gregale.dev","service":"http://127.0.0.1:8090".*"service":"http_status:404"' "$ROOT/log" \
  || fail "ingress not set: $(cat "$ROOT/log")"
grep -q '^POST .*/zones/zone/dns_records .*"name":"app-edge.gregale.dev","content":"tun-new.cfargotunnel.com","proxied":true' "$ROOT/log" \
  || fail "origin record not created: $(cat "$ROOT/log")"
! grep -q 'SECRET-CONNECTOR-TOKEN' "$ROOT/log" || fail "the token was sent back to the API"

# A re-run reuses the tunnel and updates the record in place.
run env MOCK_TUNNEL=tun-old MOCK_RECORD=rec-1 >/dev/null
! grep -q '^POST .*/cfd_tunnel ' "$ROOT/log" || fail "an existing tunnel was created again"
grep -q '^PUT .*/zones/zone/dns_records/rec-1 .*"content":"tun-old.cfargotunnel.com"' "$ROOT/log" \
  || fail "existing record not updated: $(cat "$ROOT/log")"

# Without the account id the script refuses before any call.
if PATH="$ROOT/bin:$PATH" MOCK_LOG="$ROOT/log" CF_ZONE_ID=zone CF_API_TOKEN=x bash "$SETUP" 2>/dev/null; then
  fail "ran without CF_ACCOUNT_ID"
fi

echo "cloudflare-app-edge-tunnel_test: OK"
