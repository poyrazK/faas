#!/usr/bin/env bash
# Fixture tests for canary_alert.sh.
#
# The script's whole job is to produce a body Alertmanager will route to
# faas-page. These run it against a throwaway HTTP server, capture the posted
# body, and assert on it — the alternative, eyeballing a heredoc, is how a
# canary ends up firing into a receiver that does not exist.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="${here}/canary_alert.sh"
fails=0

command -v jq >/dev/null || { echo "SKIP: jq is required" >&2; exit 0; }
command -v python3 >/dev/null || { echo "SKIP: python3 is required" >&2; exit 0; }

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"; [[ -n "${server_pid:-}" ]] && kill "$server_pid" 2>/dev/null' EXIT

capture="$workdir/body.json"
port_file="$workdir/port"

cat > "$workdir/server.py" <<'PY'
import http.server
import sys
import threading

capture, port_file = sys.argv[1], sys.argv[2]


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        with open(capture, "wb") as handle:
            handle.write(self.rfile.read(length))
        with open(capture + ".path", "w") as handle:
            handle.write(self.path)
        self.send_response(200)
        self.end_headers()

    def log_message(self, *_args):
        pass


server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w") as handle:
    handle.write(str(server.server_address[1]))
threading.Thread(target=server.serve_forever, daemon=True).start()
threading.Event().wait()
PY

python3 "$workdir/server.py" "$capture" "$port_file" &
server_pid=$!
for _ in $(seq 1 50); do
  [[ -s "$port_file" ]] && break
  sleep 0.1
done
[[ -s "$port_file" ]] || { echo "FAIL: fixture server never bound a port" >&2; exit 1; }
url="http://127.0.0.1:$(cat "$port_file")"

check() {
  local name="$1" filter="$2"
  if jq -e "$filter" "$capture" >/dev/null 2>&1; then
    echo "ok: $name"
  else
    echo "FAIL: $name" >&2
    jq -C . "$capture" 2>/dev/null | sed 's/^/    /' >&2 || cat "$capture" >&2
    fails=$((fails + 1))
  fi
}

# --- a normal failure ------------------------------------------------------
ALERTMANAGER_URL="$url" CANARY_RUN_URL="https://example.test/run/1" \
  bash "$script" "canary failed" "two deployments did not go live" >/dev/null

check "posts to the v2 alerts API" 'true' # body parsed at all
[[ "$(cat "${capture}.path")" == "/api/v2/alerts" ]] && echo "ok: v2 alerts path" || {
  echo "FAIL: posted to $(cat "${capture}.path"), want /api/v2/alerts" >&2
  fails=$((fails + 1))
}
check "is a JSON array of one alert" 'type == "array" and length == 1'
# severity=page is the label the Alertmanager route matches to reach
# faas-page (email + Pushover). Everything else is cosmetic; this is not.
check "severity is page so it reaches faas-page" '.[0].labels.severity == "page"'
check "carries alertname and component for grouping" \
  '.[0].labels.alertname == "FaasSyntheticCanaryFailed" and .[0].labels.component == "platform"'
check "carries the family label used by the other rules" \
  '.[0].labels.family == "synthetic_canary"'
check "summary and description survive" \
  '.[0].annotations.summary == "canary failed" and (.[0].annotations.description | test("two deployments"))'
check "links back to the run" '.[0].annotations.run_url == "https://example.test/run/1"'
check "sets an expiry so a passing run lets it lapse" \
  '(.[0].endsAt | length) > 0 and .[0].endsAt > .[0].startsAt'

# --- hostile text must not break the JSON ----------------------------------
rm -f "$capture"
ALERTMANAGER_URL="$url" bash "$script" \
  'quote " and brace }' 'line one
line two with '"'"'quotes'"'"' and a \backslash' >/dev/null

check "a summary containing a quote stays valid JSON" \
  '.[0].annotations.summary == "quote \" and brace }"'
check "a multi-line description with quotes survives" \
  '(.[0].annotations.description | test("line two")) and (.[0].annotations.description | test("\n"))'

# --- argument validation ---------------------------------------------------
if ALERTMANAGER_URL="$url" bash "$script" "only one arg" >/dev/null 2>&1; then
  echo "FAIL: missing description was accepted" >&2
  fails=$((fails + 1))
else
  echo "ok: refuses a missing description"
fi

if ALERTMANAGER_URL="$url" CANARY_TTL_MINUTES="soon" bash "$script" a b >/dev/null 2>&1; then
  echo "FAIL: non-numeric TTL was accepted" >&2
  fails=$((fails + 1))
else
  echo "ok: refuses a non-numeric TTL"
fi

# --- an unreachable Alertmanager must fail loudly, not silently ------------
if ALERTMANAGER_URL="http://127.0.0.1:1" CANARY_TTL_MINUTES=1 \
    bash "$script" a b >/dev/null 2>&1; then
  echo "FAIL: an unreachable Alertmanager was reported as success" >&2
  fails=$((fails + 1))
else
  echo "ok: an unreachable Alertmanager is an error"
fi

if (( fails > 0 )); then
  echo "canary_alert_test: ${fails} failure(s)" >&2
  exit 1
fi
echo "canary_alert_test: all passed"
