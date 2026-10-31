#!/usr/bin/env bash
# Exercise cloudflare-app-dns.sh against a fake Cloudflare API
# (ADR-20261030115139). Without APPLY=1 nothing is written; with it, only the
# records and routes that need a change are written; the token never prints.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DNS="$SCRIPT_DIR/cloudflare-app-dns.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# The fake curl logs "METHOD URL DATA" per call. Records live in
# $MOCK_DIR/records/<name> as "id type content proxied" lines; Worker routes
# in $MOCK_DIR/routes as "id pattern script" lines ("-" for no Worker).
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
  "GET "*"/dns_records?name="*)
    name=$(sed 's/%2A/*/g' <<<"${url##*name=}")
    file="$MOCK_DIR/records/$name"
    if [[ -f "$file" ]]; then
      jq -Rn '[inputs | split(" ") | {id: .[0], type: .[1], content: .[2], proxied: (.[3] == "true")}] | {result: .}' <"$file"
    else
      printf '{"result":[]}'
    fi ;;
  "GET "*"/dns_records?per_page="*)
    for file in "$MOCK_DIR"/records/*; do
      sed "s|^|$(basename "$file") |" "$file"
    done | jq -Rn '[inputs | split(" ") | {name: .[0], id: .[1], type: .[2], content: .[3], proxied: (.[4] == "true")}] | {result: .}' ;;
  "GET "*"/workers/routes")
    jq -Rn '[inputs | split(" ") | {id: .[0], pattern: .[1]} + (if .[2] == "-" then {} else {script: .[2]} end)] | {result: .}' <"$MOCK_DIR/routes" ;;
  "POST "*|"PUT "*|"DELETE "*) printf '{"success":true}' ;;
  *) echo "unexpected call: $method $url" >&2; exit 22 ;;
esac
MOCK
chmod +x "$ROOT/bin/curl"

# reset writes the production-like starting state: the apex on another
# provider, api. on the origin's address, no operations. or s3. record, the
# wildcard on the control plane, and the Worker on every route.
reset() {
  rm -rf "$ROOT/mock" && mkdir -p "$ROOT/mock/records"
  local r="$ROOT/mock/records"
  echo "rec-apex A 76.76.21.21 true" >"$r/example.dev"
  echo "rec-api A 203.0.113.4 true" >"$r/api.example.dev"
  echo "rec-wild CNAME origin.example.dev true" >"$r/*.example.dev"
  echo "rec-origin A 203.0.113.4 false" >"$r/origin.example.dev"
  echo "rec-edge CNAME tun-1.cfargotunnel.com true" >"$r/app-edge.example.dev"
  printf '%s\n' "route-wild *.example.dev/* gregale-public-timeout" "route-api api.example.dev/* gregale-public-timeout" >"$ROOT/mock/routes"
  : >"$ROOT/mock/log"
}
run() {
  PATH="$ROOT/bin:$PATH" MOCK_DIR="$ROOT/mock" CF_ZONE_ID=zone CF_API_TOKEN=SECRET-API-TOKEN \
    APPS_DOMAIN=example.dev "$@"
}
writes() { grep -E '^(POST|PUT|DELETE) ' "$ROOT/mock/log" || true; }
fail() { echo "FAIL: $*" >&2; exit 1; }

# plan and a dry run write nothing.
reset
out="$(run bash "$DNS" plan)"
[[ "$out" == *"route-wild *.example.dev/* gregale-public-timeout"* ]] || fail "plan does not list routes: $out"
[[ "$out" == *"  api.example.dev A 203.0.113.4 true"* ]] || fail "plan does not list every record: $out"
out="$(run bash "$DNS" platform)"
[[ -z "$(writes)" ]] || fail "a dry run wrote: $(writes)"
[[ "$out" == *"ok: example.dev has its own record (A 76.76.21.21), left alone"* ]] || fail "an apex served elsewhere must be left alone: $out"
[[ "$out" == *"ok: api.example.dev has its own record (A 203.0.113.4), left alone"* ]] || fail "api. should be left alone: $out"
[[ "$out" == *"would: operations.example.dev: new CNAME origin.example.dev"* ]] || fail "missing operations. not planned: $out"

# The wildcard waits for the platform hosts.
if run bash "$DNS" wildcard >/dev/null 2>&1; then fail "wildcard moved before the platform hosts"; fi

# APPLY=1 writes only what needs a change.
out="$(run env APPLY=1 bash "$DNS" platform)"
[[ "$out" != *SECRET-API-TOKEN* ]] || fail "the token was printed"
[[ "$(writes | grep -c '^POST .*/dns_records ')" == 2 ]] || fail "operations. and s3. not created: $(writes)"
! writes | grep -q -E 'rec-apex|rec-api|^PUT ' || fail "an existing record was rewritten: $(writes)"

# A pilot adds a route with no Worker and a record to the tunnel.
reset
run env APPLY=1 bash "$DNS" pilot h10.example.dev >/dev/null
writes | grep -q '^POST .*/workers/routes {"pattern":"h10.example.dev/\*"}$' || fail "no Worker-free route: $(writes)"
writes | grep -q '^POST .*/dns_records .*"name":"h10.example.dev".*"content":"tun-1.cfargotunnel.com".*"proxied":true' || fail "no tunnel record: $(writes)"

# Once every platform host has its own record, the wildcard moves to the tunnel.
reset
for host in operations.example.dev s3.example.dev; do
  echo "rec-$host CNAME origin.example.dev true" >"$ROOT/mock/records/$host"
done
run env APPLY=1 bash "$DNS" wildcard >/dev/null
writes | grep -q '^PUT .*/dns_records/rec-wild .*"content":"tun-1.cfargotunnel.com"' || fail "wildcard not moved: $(writes)"

# unwildcard and keep point back at the control-plane origin.
reset
echo "rec-wild CNAME tun-1.cfargotunnel.com true" >"$ROOT/mock/records/*.example.dev"
run env APPLY=1 bash "$DNS" unwildcard >/dev/null
run env APPLY=1 bash "$DNS" keep slow-app.example.dev >/dev/null
writes | grep -q '^PUT .*/dns_records/rec-wild .*"content":"origin.example.dev"' || fail "wildcard not moved back: $(writes)"
writes | grep -q '^POST .*/dns_records .*"name":"slow-app.example.dev".*"content":"origin.example.dev"' || fail "kept host has no record: $(writes)"

# drop-worker-routes deletes only routes that run a Worker.
reset
echo "route-pilot h10.example.dev/* -" >>"$ROOT/mock/routes"
run env APPLY=1 bash "$DNS" drop-worker-routes >/dev/null
[[ "$(writes | grep -c '^DELETE ')" == 2 ]] || fail "want two Worker routes deleted: $(writes)"
! writes | grep -q route-pilot || fail "a Worker-free route was deleted"

echo "cloudflare-app-dns_test: OK"
