#!/usr/bin/env bash
# ADR-1343 app edge pilot evidence (docs/runbooks/AppEdgeCutover.md §5), run
# from an operator workstation once the public-timeout Worker routes
# PILOT_HOST to the tunnel. It checks:
#
#   1. every compute edge and tunnel connector is ready;
#   2. the control plane carries none of the pilot's requests: its
#      gatewayd-public opens one TCP connection per request it routes to
#      compute (keep-alives off), so its ActiveOpens counter grows with the
#      unlisted CONTROL_HOST's requests and not with the pilot's;
#   3. no control-plane Postgres statement runs once per pilot request
#      (pg_stat_statements, minus an idle window of the same length);
#   4. the edges neither fell back to the compute pool nor answered 421;
#   5. every pilot request got an answer from the app (no 5xx, no transport
#      error);
#   6. optionally (ECHO_PATH), the app sees the caller's address, not
#      127.0.0.1 or a client-supplied X-Forwarded-For.
#
# Nothing is changed on any host. Exits 1 when a check fails, 2 when the
# routing check is inconclusive (the control host did not move the counter).
set -euo pipefail

: "${PILOT_HOST:?set PILOT_HOST to the host the Worker routes to the tunnel}"
: "${CONTROL_HOST:?set CONTROL_HOST to a warm app host the Worker does not route to the tunnel}"
REQUESTS=${REQUESTS:-100}
PILOT_PATH=${PILOT_PATH:-/}
ECHO_PATH=${ECHO_PATH:-}
NODE_SSH=${NODE_SSH:-$HOME/gregale-e2e/bin/gpssh}
CP_NODE=${CP_NODE:-gregale-cp}
COMPUTE_NODES=${COMPUTE_NODES:-gregale-compute-1 gregale-compute-2}
CURL=${CURL:-curl}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

failed=0
fail() { echo "FAIL: $*"; failed=1; }
pass() { echo "PASS: $*"; }

# remote <node> <script>: run a bash script as root on a node.
remote() { "$NODE_SSH" "$1" 'sudo bash -s' <<<"$2" 2>/dev/null; }

# cp_snapshot prints "opens <ActiveOpens>" then "<queryid>\t<calls>" per
# statement (no statement lines when pg_stat_statements is missing).
# shellcheck disable=SC2016 # the script runs remotely; nothing expands here
cp_snapshot() {
  remote "$CP_NODE" '
awk '\''/^Tcp:/ { if (!h) { for (i = 1; i <= NF; i++) col[$i] = i; h = 1 } else print "opens", $col["ActiveOpens"] }'\'' /proc/net/snmp
sudo -u postgres psql -d postgres -XAtF "	" -c "select queryid, calls from pg_stat_statements" 2>/dev/null || true
'
}

# edge_counters prints "<node> <fallbacks> <misdirected>" summed over labels.
edge_counters() {
  local node
  for node in $COMPUTE_NODES; do
    remote "$node" "curl -fsS --max-time 5 http://127.0.0.1:9092/metrics | awk -v node=$node '
      /^gatewayd_public_edge_local_fallback_total/ { f += \$NF }
      /^gatewayd_public_edge_misdirected_total/ { m += \$NF }
      END { print node, f + 0, m + 0 }'"
  done
}

# burst <host> sends REQUESTS sequential requests and prints status counts.
burst() {
  local host=$1 i code
  for ((i = 0; i < REQUESTS; i++)); do
    code=$("$CURL" -s -o /dev/null -w '%{http_code}' --max-time 30 "https://$host$PILOT_PATH" || true)
    echo "${code:-000}"
  done | sort | uniq -c | awk '{ printf "%s=%s ", $2, $1 } END { print "" }'
}

opens_of() { awk '$1 == "opens" { print $2 }' <<<"$1"; }

echo "== 1. edges ready"
if "$NODE_SSH" "$CP_NODE" 'bash -s' <"$SCRIPT_DIR/faas-check-compute-edges.sh" 2>/dev/null; then
  pass "every compute edge and connector is ready"
else
  fail "a compute edge or connector is not ready (see above)"
fi

echo "== warm up"
"$CURL" -s -o /dev/null --max-time 30 "https://$PILOT_HOST$PILOT_PATH" || true
"$CURL" -s -o /dev/null --max-time 30 "https://$CONTROL_HOST$PILOT_PATH" || true

edges_before=$(edge_counters)
snap0=$(cp_snapshot)
start=$SECONDS
pilot_codes=$(burst "$PILOT_HOST")
window=$((SECONDS - start))
snap1=$(cp_snapshot)
edges_after=$(edge_counters)
sleep "$window"
snap2=$(cp_snapshot)
control_codes=$(burst "$CONTROL_HOST")
snap3=$(cp_snapshot)

for snap in "$snap0" "$snap1" "$snap2" "$snap3"; do
  [[ -n "$(opens_of "$snap")" ]] || { echo "FAIL: could not read $CP_NODE's counters"; exit 1; }
done

echo "== 2. the control plane carries no pilot request"
idle=$(($(opens_of "$snap2") - $(opens_of "$snap1")))
pilot=$(($(opens_of "$snap1") - $(opens_of "$snap0") - idle))
control=$(($(opens_of "$snap3") - $(opens_of "$snap2") - idle))
echo "connections opened by the control plane beyond an idle ${window}s window: pilot $pilot, control $control (of $REQUESTS each)"
if ((control * 10 < REQUESTS * 8)); then
  echo "INCONCLUSIVE: the control host did not move the counter; is $CONTROL_HOST warm and served through the control plane?"
  [[ $failed == 1 ]] || failed=2
elif ((pilot * 10 > REQUESTS * 2)); then
  fail "the control plane opened $pilot connections for $REQUESTS pilot requests"
else
  pass "the pilot's requests did not pass through the control plane"
fi

echo "== 3. no control-plane Postgres statement per request"
if ! grep -q $'\t' <<<"$snap0"; then
  echo "SKIP: pg_stat_statements is not readable on $CP_NODE"
else
  per_request=$(python3 - "$REQUESTS" <(echo "$snap0") <(echo "$snap1") <(echo "$snap2") <<'EOF'
import sys
n = int(sys.argv[1])
def load(path):
    calls = {}
    for line in open(path):
        parts = line.rstrip("\n").split("\t")
        if len(parts) == 2 and parts[1].isdigit():
            calls[parts[0]] = calls.get(parts[0], 0) + int(parts[1])
    return calls
s0, s1, s2 = (load(p) for p in sys.argv[2:5])
for q, c1 in s1.items():
    burst = c1 - s0.get(q, 0)
    idle = s2.get(q, c1) - c1
    if burst - max(idle, 0) >= n * 0.8:
        print(f"{q}\t{burst - max(idle, 0)}")
EOF
)
  if [[ -n "$per_request" ]]; then
    fail "statements that ran about once per pilot request (queryid, extra calls):"
    echo "$per_request"
  else
    pass "no statement ran once per pilot request"
  fi
fi

echo "== 4. edges answered locally"
for node in $COMPUTE_NODES; do
  read -r _ f0 m0 < <(awk -v n="$node" '$1 == n' <<<"$edges_before") || true
  read -r _ f1 m1 < <(awk -v n="$node" '$1 == n' <<<"$edges_after") || true
  if [[ -z "${f0:-}" || -z "${f1:-}" ]]; then
    fail "$node: edge metrics unreadable"
  elif ((f1 > f0 || m1 > m0)); then
    fail "$node: $((f1 - f0)) local-gateway fallbacks, $((m1 - m0)) misdirected (421)"
  else
    pass "$node: no fallback, no 421"
  fi
  unset f0 m0 f1 m1
done

echo "== 5. every pilot request answered"
echo "pilot: $pilot_codes"
echo "control: $control_codes"
if grep -qE '(^| )(000|5[0-9][0-9])=' <<<"$pilot_codes"; then
  fail "pilot requests got 5xx or transport errors"
else
  pass "no 5xx or transport error on the pilot host"
fi

if [[ -n "$ECHO_PATH" ]]; then
  echo "== 6. client address"
  me=$("$CURL" -fsS --max-time 10 "https://$PILOT_HOST/cdn-cgi/trace" | awk -F= '$1 == "ip" { print $2 }')
  seen=$("$CURL" -fsS --max-time 30 -H 'X-Forwarded-For: 203.0.113.9' "https://$PILOT_HOST$ECHO_PATH" || true)
  if [[ -n "$me" && "$seen" == *"$me"* && "$seen" != *203.0.113.9* ]]; then
    pass "the app sees $me and not the forged X-Forwarded-For"
  else
    fail "the app did not see the caller's address $me (or saw the forged one): ${seen:0:300}"
  fi
fi

echo "== not automated: §5 checks 4 (timeout body), 5 (public object reads), 6 (streams), 7 (drain) and 8 (load)"
exit "$failed"
