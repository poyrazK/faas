#!/usr/bin/env bash
# Probe the app edge on every active compute node from the control plane
# (ADR-1343 §5). The edge's public listener is loopback-only on each node, so
# this checks what the control plane can reach: gatewayd-public's readiness on
# its control port and the Cloudflare Tunnel connector's readiness on its
# metrics port. A public request cannot pick its connector, so the public
# endpoint check alone cannot see one bad node.
#
# Run on the control plane (make check-compute-edges). Exits non-zero when
# any edge or connector is not ready, or when discovery lists no edge.
set -euo pipefail

discovery_url="${FAAS_EDGE_DISCOVERY_URL:-http://127.0.0.1:9101/v1/internal/metrics/gatewayd-public-edge-targets}"
connector_port="${FAAS_EDGE_CONNECTOR_METRICS_PORT:-20241}"
curl_bin="${FAAS_CURL:-curl}"

discovery="$("$curl_bin" --fail --silent --show-error --max-time 10 "$discovery_url")" \
  || { echo "compute-edge-check: edge discovery at ${discovery_url} failed" >&2; exit 2; }

# One "node host:port" line per discovered edge.
targets="$(python3 -c '
import json, sys
for group in json.loads(sys.argv[1]):
    for target in group.get("targets", []):
        print(group.get("labels", {}).get("node", "?"), target)
' "$discovery")"
if [[ -z "$targets" ]]; then
  echo "compute-edge-check: discovery lists no compute edge" >&2
  exit 1
fi

failed=0
while read -r node target; do
  host="${target%:*}"
  edge="ready"
  if ! "$curl_bin" --fail --silent --max-time 5 --output /dev/null "http://${target}/readyz"; then
    edge="NOT-READY"
    failed=1
  fi
  connector="connected"
  if ! "$curl_bin" --fail --silent --max-time 5 --output /dev/null "http://${host}:${connector_port}/ready"; then
    connector="NOT-CONNECTED"
    failed=1
  fi
  printf '%-32s edge=%-9s connector=%s\n' "$node" "$edge" "$connector"
done <<<"$targets"
exit "$failed"
