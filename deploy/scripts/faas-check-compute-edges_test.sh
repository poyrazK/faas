#!/usr/bin/env bash
# Exercise faas-check-compute-edges.sh against a fake curl (ADR-1343 §5).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/faas-check-compute-edges.sh"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT

# The fake curl answers the discovery URL with MOCK_DISCOVERY and fails any
# URL listed in MOCK_DOWN (space-separated substrings).
cat >"$ROOT/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
if [[ "$url" == *targets* ]]; then
  printf '%s' "$MOCK_DISCOVERY"
  exit 0
fi
for down in $MOCK_DOWN; do
  [[ "$url" == *"$down"* ]] && exit 22
done
exit 0
EOF
chmod +x "$ROOT/curl"

discovery='[{"targets":["10.0.0.2:9092"],"labels":{"job":"gatewayd-public-edge","node":"cn-1"}},{"targets":["10.0.0.3:9092"],"labels":{"job":"gatewayd-public-edge","node":"cn-2"}}]'
run() {
  FAAS_CURL="$ROOT/curl" MOCK_DISCOVERY="$1" MOCK_DOWN="$2" bash "$CHECK"
}

out="$(run "$discovery" "")"
[[ "$out" == *"cn-1"*"edge=ready"*"connector=connected"* && "$out" == *"cn-2"* ]] \
  || { echo "FAIL: healthy edges not reported: $out" >&2; exit 1; }

if out="$(run "$discovery" "10.0.0.3:20241")"; then
  echo "FAIL: a disconnected connector passed" >&2; exit 1
fi
[[ "$out" == *"cn-2"*"connector=NOT-CONNECTED"* ]] || { echo "FAIL: cn-2 connector not flagged: $out" >&2; exit 1; }

if out="$(run "$discovery" "10.0.0.2:9092/readyz")"; then
  echo "FAIL: an unready edge passed" >&2; exit 1
fi
[[ "$out" == *"cn-1"*"edge=NOT-READY"* ]] || { echo "FAIL: cn-1 edge not flagged: $out" >&2; exit 1; }

if run "[]" "" >/dev/null 2>&1; then
  echo "FAIL: an empty discovery passed" >&2; exit 1
fi
echo "PASS: compute edge check reports each node's edge and connector"
