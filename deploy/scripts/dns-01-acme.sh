#!/usr/bin/env bash
# dns-01-acme.sh — idempotent ACME DNS-01 zone bootstrap for gatewayd-public TLS.
#
# PR-8: renamed from hetzner-zone-setup.sh. The script body still targets the
# Hetzner DNS API (https://dns.hetzner.com/api/v1) because that is the only
# DNS-01 provider we currently operate; pkg/gateway/tls_wire.go dispatches on
# FAAS_DNS_PROVIDER (cloudflare|route53|hetzner|manual) — see PR-8 commit 2.
# Future ops follow-up (PR-9 candidate) will parameterize this script on the
# provider so the same shell wrapper covers Cloudflare / Route53 too.
#
# Spec §4.1: gatewayd-public terminates TLS for *.gregale.dev via DNS-01 against
# the DNS provider API. Three records have to exist before the first
# daemon start:
#
#   gregale.dev         A     <production host public IP>   wildcard cert cover
#   edge.gregale.dev    CNAME <production host public IP>   customer-facing alias (HTTP-01)
#   _faas-verify   TXT   faas-domain-ok=1   proof-of-control marker for
#                                           DNS-01 sanity checks (optional)
#
# The script is idempotent — running it twice does not duplicate records
# and does not error if the zone doesn't yet exist (it creates the zone
# if CREATE_ZONE=1). Reads the API token from
# /etc/faas/secrets/hetzner-dns.token by default (the same path the
# daemon reads); pass --token-file to override.
#
# Usage:
#   sudo bash dns-01-acme.sh \
#       --zone gregale.dev \
#       --apps-domain gregale.dev \
#       --edge-host edge.gregale.dev \
#       --host-ip 1.2.3.4 \
#       [--token-file /etc/faas/secrets/hetzner-dns.token] \
#       [--verify-record _faas-verify] \
#       [--create-zone]
#
# What it does NOT do:
#   - delegate the apex NS to the DNS provider (operator action in registrar UI)
#   - set up DNSSEC (out of scope for v1; revisit when provider supports it)
#   - mint the wildcard cert itself (that's the daemon's job on first boot)
#
# Dependencies: bash, curl, python3 (parses JSON responses).
# The script fails fast with a clear error if python3 is missing.

set -euo pipefail

# Required tools. python3 parses JSON responses (jq is an option
# but python3 ships in the deploy base image already, so we standardize on
# it). Fail fast with a useful message rather than the cryptic `python3:
# command not found` mid-script.
for cmd in python3 curl; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "missing required command: $cmd" >&2
        echo "(install with: apt-get install -y ${cmd/python3/python3-minimal}  # or equivalent for your distro)" >&2
        exit 1
    fi
done

ZONE=""
APPS_DOMAIN=""
EDGE_HOST=""
HOST_IP=""
TOKEN_FILE="/etc/faas/secrets/hetzner-dns.token"
VERIFY_RECORD="_faas-verify"
# PR-8: renamed from HETZNER_CREATE_ZONE — the flag is now provider-agnostic
# inside the deploy/scripts surface. Operators on the same shell keep the
# same behaviour with the new env name.
CREATE_ZONE="${DNS01_CREATE_ZONE:-0}"

usage() {
    sed -n '2,29p' "$0"
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --zone)           ZONE="$2";           shift 2 ;;
        --apps-domain)    APPS_DOMAIN="$2";    shift 2 ;;
        --edge-host)      EDGE_HOST="$2";      shift 2 ;;
        --host-ip)        HOST_IP="$2";        shift 2 ;;
        --token-file)     TOKEN_FILE="$2";     shift 2 ;;
        --verify-record)  VERIFY_RECORD="$2";  shift 2 ;;
        --create-zone)    CREATE_ZONE=1;       shift ;;
        -h|--help)        usage ;;
        *)                echo "unknown flag: $1" >&2; usage ;;
    esac
done

if [[ -z "$ZONE" || -z "$APPS_DOMAIN" || -z "$EDGE_HOST" || -z "$HOST_IP" ]]; then
    echo "missing required flag (need --zone --apps-domain --edge-host --host-ip)" >&2
    usage
fi

# Validate --host-ip is a dotted-quad IPv4. The runbook assumes v4 for
# the A record (the DNS-01 ACME challenge path serves the gatewayd-public
# public IP). A typo here (e.g. "1.2.3,4") would silently land a broken
# record and DNS would fail later with a cryptic SERVFAIL.
if ! [[ "$HOST_IP" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    echo "--host-ip $HOST_IP is not a dotted-quad IPv4 (e.g. 1.2.3.4)" >&2
    exit 1
fi
# Range-check each octet (regex above permits 0-999 per octet; tighten
# to the legal 0-255). Done as a Python one-liner because bash doesn't
# have a clean octet-comparison primitive and we already depend on python3.
if ! python3 -c '
import sys
ip = sys.argv[1]
parts = ip.split(".")
if len(parts) != 4 or not all(0 <= int(p) <= 255 for p in parts):
    sys.exit(1)
' "$HOST_IP"; then
    echo "--host-ip $HOST_IP has an octet outside 0-255" >&2
    exit 1
fi

if [[ ! -r "$TOKEN_FILE" ]]; then
    echo "token file $TOKEN_FILE not readable" >&2
    exit 1
fi
TOKEN="$(tr -d '[:space:]' < "$TOKEN_FILE")"
if [[ -z "$TOKEN" ]]; then
    echo "token file $TOKEN_FILE is empty" >&2
    exit 1
fi

# Strip any leading wildcard dots — the API wants the bare zone name.
ZONE_BARE="${ZONE%.}"

HETZNER_API="https://dns.hetzner.com/api/v1"

# hz_request <method> <path> [body] — wraps curl with auth + JSON.
hz_request() {
    local method="$1" path="$2" body="${3:-}"
    local args=(-sS -X "$method" -H "Auth-API-Token: $TOKEN" -H "Content-Type: application/json")
    if [[ -n "$body" ]]; then
        args+=(--data "$body")
    fi
    curl "${args[@]}" "$HETZNER_API$path"
}

# Find the zone ID for a zone name. Empty stdout if missing.
hz_zone_id() {
    hz_request GET "/zones?name=$ZONE_BARE" \
        | python3 -c '
import json, sys
data = json.load(sys.stdin)
for z in data.get("zones", []):
    if z.get("name") == sys.argv[1]:
        print(z["id"])
        break
' "$ZONE_BARE"
}

# Create a record of the given type, name, value. Idempotent: if a
# record with the same type+name already exists, update its value
# rather than erroring.
hz_upsert_record() {
    local zone_id="$1" rtype="$2" rname="$3" rvalue="$4"
    local existing_id
    existing_id=$(hz_request GET "/records?zone_id=$zone_id" \
        | python3 -c '
import json, sys
data = json.load(sys.stdin)
for r in data.get("records", []):
    if r.get("type") == sys.argv[1] and r.get("name") == sys.argv[2]:
        print(r["id"])
        break
' "$rtype" "$rname")
    if [[ -n "$existing_id" ]]; then
        echo "  update: $rname $rtype -> $rvalue (id=$existing_id)"
        hz_request PUT "/records/$existing_id" \
            "{\"zone_id\":\"$zone_id\",\"type\":\"$rtype\",\"name\":\"$rname\",\"value\":\"$rvalue\",\"ttl\":300}"
    else
        echo "  create: $rname $rtype -> $rvalue"
        hz_request POST "/records" \
            "{\"zone_id\":\"$zone_id\",\"type\":\"$rtype\",\"name\":\"$rname\",\"value\":\"$rvalue\",\"ttl\":300}"
    fi
}

echo "==> resolving zone $ZONE_BARE"
ZONE_ID="$(hz_zone_id || true)"
if [[ -z "$ZONE_ID" ]]; then
    if [[ "$CREATE_ZONE" == "1" ]]; then
        echo "==> creating zone $ZONE_BARE"
        ZONE_ID=$(hz_request POST "/zones" "{\"name\":\"$ZONE_BARE\"}" \
            | python3 -c 'import json,sys; print(json.load(sys.stdin)["zone"]["id"])')
    else
        echo "zone $ZONE_BARE not found; pass --create-zone or set DNS01_CREATE_ZONE=1" >&2
        echo "(if this is a new zone, also delegate the apex NS at the registrar to the provider's nameservers first)" >&2
        exit 1
    fi
fi
echo "    zone_id=$ZONE_ID"

# The wildcard A: certmagic wants *.<zone> to resolve to the
# gatewayd-public box. We write the bare zone name; the DNS API
# serves both apex and wildcard lookups from that record, so the
# wildcard `*` prefix is not a record on its own.
echo "==> A $APPS_DOMAIN -> $HOST_IP"
hz_upsert_record "$ZONE_ID" "A" "$APPS_DOMAIN" "$HOST_IP"

# Edge CNAME: customer-facing alias (HTTP-01 challenge path).
echo "==> CNAME $EDGE_HOST -> $APPS_DOMAIN"
hz_upsert_record "$ZONE_ID" "CNAME" "$EDGE_HOST" "$APPS_DOMAIN"

# Verification TXT (optional — operators can skip if their zone already
# has a sanity-check record).
if [[ -n "$VERIFY_RECORD" ]]; then
    echo "==> TXT $VERIFY_RECORD -> faas-domain-ok=1"
    hz_upsert_record "$ZONE_ID" "TXT" "$VERIFY_RECORD" '"faas-domain-ok=1"'
fi

echo "==> done. validate with:"
echo "    dig +short $APPS_DOMAIN"
echo "    dig +short $EDGE_HOST"
echo "    dig +short $VERIFY_RECORD TXT"
