#!/usr/bin/env bash
# Load the compute nodes' public edges with no app behind them and print a
# pass/fail readout per step (docs/runbooks/EdgeLoadTest.md).
#
# For each scenario of edge.js and each rate step, one k6 process per node
# drives that node's entry address, and the runner reads, per node:
#   - answers by status, achieved rate, p50 and p99 (k6);
#   - CPU of faas-gatewayd-public-edge and faas-gatewayd-internal, as cores
#     and as microseconds per request net of an idle baseline taken before the
#     first step; node busy;
#   - the edge's client-limit refusals, gatewayd-internal's unknown-host
#     answers (remembered_miss, shed) and its database pool acquires;
# and control-plane Postgres CPU pressure every 5 s. Pressure (avg10) above
# CP_STALL_ABORT_PCT stops the step at once, and no step starts while it is
# above CP_CALM_PCT.
#
# A step passes when every answer was 404 (NO_ANSWER_MAX requests with no
# answer at all are allowed for the client's own network), p99 is at most
# P99_MAX_MS, every node stayed under NODE_BUSY_MAX percent busy, nothing was
# refused or shed, k6 reached 95% of the rate, and Postgres was stalled less
# than half of the step. The first failing step ends the run: the next step
# would load harder a platform that just failed.
#
# Rates are per node and must stay under the per-address client limits and
# the new-host lookup budget (pkg/api/limits.go), so a step measures the edge
# rather than its refusals. Steps start 5 to 12 minutes past a quarter hour,
# away from the cron waves, unless AVOID_CRON_WAVES=0.
#
# Needs k6, jq and curl; NODES, a list of "<ssh host>=<entry IPv4>"; DOMAIN,
# the apps domain; and NODE_SSH, which runs a command on a host with stdin
# forwarded: NODE_SSH <host> <command>.
set -euo pipefail

: "${NODES:?set NODES to \"<ssh host>=<entry IPv4> ...\"}"
: "${DOMAIN:?set DOMAIN to the apps domain}"
NODE_SSH=${NODE_SSH:-$HOME/gregale-e2e/bin/gpssh}
CP=${CP:-gregale-cp}
K6=${K6:-k6}
CURL=${CURL:-curl}
SCENARIOS=${SCENARIOS:-miss handshake new-host}
MISS_RATES=${MISS_RATES:-50 100 200 300}
HANDSHAKE_RATES=${HANDSHAKE_RATES:-20 40 60}
NEW_HOST_RATES=${NEW_HOST_RATES:-5 10 20}
DURATION_S=${DURATION_S:-60}
RAMP_S=${RAMP_S:-10}
BASELINE_S=${BASELINE_S:-30}
MISS_HOST=${MISS_HOST:-lt-edge-none.$DOMAIN}
P99_MAX_MS=${P99_MAX_MS:-1500}
NODE_BUSY_MAX=${NODE_BUSY_MAX:-85}
NO_ANSWER_MAX=${NO_ANSWER_MAX:-5}
CP_STALL_ABORT_PCT=${CP_STALL_ABORT_PCT:-50}
CP_CALM_PCT=${CP_CALM_PCT:-15}
CP_CALM_TRIES=${CP_CALM_TRIES:-40}
CP_CALM_WAIT_S=${CP_CALM_WAIT_S:-15}
AVOID_CRON_WAVES=${AVOID_CRON_WAVES:-1}
EDGE_METRICS_PORT=${EDGE_METRICS_PORT:-9109}
INTERNAL_METRICS_PORT=${INTERNAL_METRICS_PORT:-9090}
POLL_S=${POLL_S:-2}
RUN_ID=${RUN_ID:-$(date -u +%H%M%S)}
OUT=${OUT:-results-edge-$(date -u +%Y%m%dT%H%M%SZ)}
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

refuse() {
  echo "edge load test: $*" >&2
  exit 2
}

# rate_cap <scenario>: the highest per-node rate a step may ask for, under
# the limit it would otherwise measure:
#   - miss: 90% of EdgeClientRequestsPerSecondPerAddr (500);
#   - handshake: EdgeTLSHandshakesInFlightPerAddrMax (32) binds before
#     EdgeClientNewConnsPerSecondPerAddr (100). Handshakes in flight are the
#     rate times the handshake's time, about 330 ms from a client 160 ms
#     away, and latency spikes double it: 80/s was refused on 2026-11-09,
#     60/s keeps about 20 in flight;
#   - new-host: 90% of GatewayNewHostLookupsPerSecond and
#     PublicReadNewHostLookupsPerSecond (25).
rate_cap() {
  case $1 in
    miss) echo 450 ;;
    handshake) echo 60 ;;
    new-host) echo 22 ;;
    *) echo 0 ;;
  esac
}
rates_for() {
  case $1 in
    miss) echo "$MISS_RATES" ;;
    handshake) echo "$HANDSHAKE_RATES" ;;
    new-host) echo "$NEW_HOST_RATES" ;;
  esac
}

for tool in "$K6" jq "$CURL"; do
  command -v "$tool" >/dev/null || refuse "needs $tool"
done
for scenario in $SCENARIOS; do
  cap=$(rate_cap "$scenario")
  ((cap > 0)) || refuse "unknown scenario $scenario (miss, handshake, new-host)"
  for rate in $(rates_for "$scenario"); do
    ((rate > 0 && rate <= cap)) || refuse "$scenario rate $rate per node is outside 1..$cap, the step would measure the limits"
  done
done
for node in $NODES; do
  [[ "$node" == *=* ]] || refuse "NODES entry $node is not <ssh host>=<entry IPv4>"
done
mkdir -p "$OUT"

snap() {
  "$NODE_SSH" "$1" "bash -s -- $EDGE_METRICS_PORT $INTERNAL_METRICS_PORT" <"$HERE/node_snapshot.sh" >"$2"
}
# val <file> <key>: the value of key in a snapshot, 0 when absent.
val() {
  awk -v k="$2" '$1 == k { v = $2; found = 1 } END { print found ? v : 0 }' "$1"
}
# delta <before> <after> <key>
delta() {
  awk -v a="$(val "$2" "$3")" -v b="$(val "$1" "$3")" 'BEGIN { printf "%.0f", a - b }'
}
# span <before> <after>: seconds between two snapshots, by the node's clock.
span() {
  awk -v a="$(val "$2" now)" -v b="$(val "$1" now)" 'BEGIN { s = a - b; printf "%.3f", (s > 0 ? s : 1) }'
}
# Sum of all "refused.*" (or any prefix) deltas between two snapshots.
delta_prefix() {
  awk -v p="$3" 'FNR == NR { if (index($1, p) == 1) b[$1] = $2; next }
    index($1, p) == 1 { d = $2 - b[$1]; if (d != 0) { printf "%s%s+%.0f", sep, substr($1, length(p) + 1), d; sep = " " } }
    END { if (sep == "") printf "none" }' "$1" "$2"
}
alive() {
  for pid in "$@"; do
    kill -0 "$pid" 2>/dev/null && return 0
  done
  return 1
}
# wait_off_cron_wave: start no step within 3 minutes before or 5 after a
# quarter hour, when cron and probe wakes load the nodes and control-plane
# Postgres (and would be read as the edge's cost). On 2026-11-09 the 17:45
# wave still held Postgres CPU pressure near 60% at 17:48.
wait_off_cron_wave() {
  ((AVOID_CRON_WAVES)) || return 0
  local m s
  while :; do
    m=$((10#$(date -u +%M) % 15))
    s=$((10#$(date -u +%S)))
    ((m >= 5 && m < 12)) && return 0
    echo "waiting for the cron wave to pass ($(date -u +%H:%M:%S)Z)"
    sleep $(((((20 - m) % 15) * 60) - s))
  done
}
# wait_cp_calm: start no step while control-plane Postgres CPU pressure
# (avg10) is above CP_CALM_PCT; a step started on a busy control plane would
# be stopped for load it did not cause. Gives up after CP_CALM_TRIES checks.
wait_cp_calm() {
  local try avg10
  for ((try = 1; try <= CP_CALM_TRIES; try++)); do
    avg10=$("$NODE_SSH" "$CP" "bash -s -- 1" <"$HERE/cp_pressure.sh" 2>/dev/null | awk 'NF == 2 { v = $1 } END { print v + 0 }')
    awk -v a="$avg10" -v m="$CP_CALM_PCT" 'BEGIN { exit !(a <= m) }' && return 0
    echo "waiting for control-plane Postgres to calm: CPU pressure avg10 ${avg10}% above ${CP_CALM_PCT}% ($(date -u +%H:%M:%S)Z)"
    sleep "$CP_CALM_WAIT_S"
  done
  return 1
}

echo "edge load test: nodes $NODES; scenarios $SCENARIOS; results in $OUT"
for node in $NODES; do
  host=${node%%=*} ip=${node#*=}
  code=$("$CURL" -sS -o /dev/null -w '%{http_code}' --max-time 10 --resolve "$MISS_HOST:443:$ip" "https://$MISS_HOST/" || true)
  [[ "$code" == 404 ]] || refuse "$MISS_HOST at $host ($ip) answered ${code:-nothing}, want 404: it must name no app"
  snap "$host" "$OUT/$host.baseline-before" || refuse "cannot read $host's counters over NODE_SSH"
  if ! grep -q '^cpu_usec.gatewayd-public-edge [0-9]' "$OUT/$host.baseline-before" || ! grep -q '^refused\.' "$OUT/$host.baseline-before"; then
    refuse "$host's snapshot lacks the edge's CPU or refusal counters: $(tr '\n' ' ' <"$OUT/$host.baseline-before")"
  fi
done
echo "idle baseline: ${BASELINE_S}s"
sleep "$BASELINE_S"
for node in $NODES; do
  snap "${node%%=*}" "$OUT/${node%%=*}.baseline-after"
done

# baseline_cores <host> <unit>: the unit's idle CPU, in cores.
baseline_cores() {
  local b="$OUT/$1.baseline-before" a="$OUT/$1.baseline-after"
  awk -v d="$(delta "$b" "$a" "cpu_usec.$2")" -v s="$(span "$b" "$a")" 'BEGIN { printf "%.4f", d / 1e6 / s }'
}

# readout_node <dir> <host> <ip> <rate>: one node's lines; prints FAIL lines
# for each criterion it misses.
readout_node() {
  local dir=$1 host=$2 ip=$3 rate=$4 b="$1/$2.before" a="$1/$2.after"
  local reqs s404 bad s429 s503 s5xx sother snone dropped p50 p99
  read -r reqs s404 bad s429 s503 s5xx sother snone dropped p50 p99 < <(jq -r '[
      .metrics.http_reqs.count // 0, .metrics.s404.count // 0, .metrics.bad.count // 0,
      .metrics.s429.count // 0, .metrics.s503.count // 0, .metrics.s5xx.count // 0,
      .metrics.sother.count // 0, .metrics.snone.count // 0, .metrics.dropped_iterations.count // 0,
      (.metrics.http_req_duration["p(50)"] // 0 | floor), (.metrics.http_req_duration["p(99)"] // 0 | floor)
    ] | @tsv' "$dir/$host.json" 2>/dev/null || echo "0 0 0 0 0 0 0 0 0 0 0")
  local w want
  w=$(span "$b" "$a")
  # The ramp starts at a fifth of the rate.
  want=$(awk -v r="$rate" -v d="$DURATION_S" -v m="$RAMP_S" 'BEGIN { printf "%.0f", r * d + (r / 5 + r) / 2 * m }')
  echo "node $host ($ip): $reqs requests of $want: 404 $s404, bad $bad (429 $s429, 503 $s503, 5xx $s5xx, other $sother, no answer $snone), dropped $dropped; p50 $p50 ms, p99 $p99 ms"
  local line="" unit cores net
  for unit in gatewayd-public-edge gatewayd-internal; do
    read -r cores net < <(awk -v d="$(delta "$b" "$a" "cpu_usec.$unit")" -v s="$w" -v base="$(baseline_cores "$host" "$unit")" -v n="$reqs" \
      'BEGIN { c = d / 1e6 / s; printf "%.2f %.0f\n", c, (n > 0 ? (c - base) * s * 1e6 / n : 0) }')
    line="$line ${unit#gatewayd-} $cores cores (net $net us/request),"
  done
  local busy
  busy=$(awk -v t="$(delta "$b" "$a" proc.total)" -v i="$(delta "$b" "$a" proc.idle)" 'BEGIN { printf "%.0f", (t > 0 ? 100 * (t - i) / t : 0) }')
  echo "  cpu$line node busy ${busy}%"
  local refused shed
  refused=$(delta_prefix "$b" "$a" refused.)
  shed=$(delta "$b" "$a" unknown_host.shed)
  echo "  refused: $refused; unknown hosts: remembered_miss +$(delta "$b" "$a" unknown_host.remembered_miss), shed +$shed; db pool acquires +$(delta "$b" "$a" db_pool.acquires)"
  ((s429 + s503 + s5xx + sother == 0)) || echo "  FAIL: answers other than 404"
  ((snone <= NO_ANSWER_MAX)) || echo "  FAIL: $snone requests got no answer (allowed $NO_ANSWER_MAX)"
  ((p99 <= P99_MAX_MS)) || echo "  FAIL: p99 $p99 ms above $P99_MAX_MS"
  ((busy <= NODE_BUSY_MAX)) || echo "  FAIL: node busy ${busy}% above ${NODE_BUSY_MAX}%"
  [[ "$refused" == none ]] || echo "  FAIL: the edge refused requests or connections"
  ((shed == 0)) || echo "  FAIL: the gateway shed unknown hosts"
  ((reqs * 100 >= want * 95)) || echo "  FAIL: k6 reached $reqs of $want requests; the client, not the edge, set the rate"
}

# readout_cp <dir>: control-plane Postgres stall over the step.
readout_cp() {
  awk -v max="$CP_STALL_ABORT_PCT" 'NF == 2 { n++; if (n == 1) { t0 = $2 }; t1 = $2; if ($1 > peak) peak = $1 }
    END {
      if (n < 2) { print "cp postgres: no pressure samples"; print "  FAIL: control-plane Postgres not observed"; exit }
      stall = (t1 - t0) / 1e6 / ((n - 1) * 5)
      printf "cp postgres: cpu stall %.1f%% of the step, peak avg10 %.1f%% (abort above %s%%)\n", 100 * stall, peak, max
      if (stall > 0.5) print "  FAIL: Postgres stalled above FaasPostgresCPUStalled (half the time)"
    }' "$1/cp-pressure.txt"
}

run_step() {
  local scenario=$1 rate=$2 dir="$OUT/$1-$2"
  mkdir -p "$dir"
  wait_off_cron_wave
  if ! wait_cp_calm; then
    echo "  FAIL: control-plane Postgres CPU pressure stayed above ${CP_CALM_PCT}%; no step started" | tee "$dir/readout.txt"
    echo "verdict $scenario $rate/s: FAIL"
    return 1
  fi
  local node host ip i=0 pids=() wpid
  for node in $NODES; do
    snap "${node%%=*}" "$dir/${node%%=*}.before"
  done
  "$NODE_SSH" "$CP" "bash -s -- $(((RAMP_S + DURATION_S + 15) / 5 + 1))" <"$HERE/cp_pressure.sh" >"$dir/cp-pressure.txt" 2>"$dir/cp-pressure.err" &
  wpid=$!
  local start
  start=$(date +%s)
  for node in $NODES; do
    host=${node%%=*} ip=${node#*=} i=$((i + 1))
    "$K6" run --quiet --no-color --summary-export "$dir/$host.json" \
      -e "SCENARIO=$scenario" -e "TARGET_IP=$ip" -e "DOMAIN=$DOMAIN" -e "MISS_HOST=$MISS_HOST" \
      -e "RUN_ID=${RUN_ID}n$i" -e "RATE=$rate" -e "DURATION=${DURATION_S}s" -e "RAMP=${RAMP_S}s" \
      "$HERE/edge.js" >"$dir/$host.k6.txt" 2>&1 &
    pids+=($!)
  done
  local aborted="" avg10
  while alive "${pids[@]}"; do
    sleep "$POLL_S"
    avg10=$(awk 'NF == 2 { v = $1 } END { print v + 0 }' "$dir/cp-pressure.txt")
    if awk -v a="$avg10" -v m="$CP_STALL_ABORT_PCT" 'BEGIN { exit !(a > m) }'; then
      aborted="control-plane Postgres CPU pressure avg10 ${avg10}% above ${CP_STALL_ABORT_PCT}%"
      kill "${pids[@]}" 2>/dev/null || true
    fi
  done
  local pid
  for pid in "${pids[@]}"; do
    wait "$pid" || true
  done
  local end
  end=$(date +%s)
  wait "$wpid" || true
  for node in $NODES; do
    snap "${node%%=*}" "$dir/${node%%=*}.after"
  done
  {
    echo "== $scenario $rate/s per node: $(date -u -r "$start" +%FT%TZ 2>/dev/null || date -u -d "@$start" +%FT%TZ) for $((end - start))s"
    for node in $NODES; do
      readout_node "$dir" "${node%%=*}" "${node#*=}" "$rate"
    done
    readout_cp "$dir"
    [[ -z "$aborted" ]] || echo "  FAIL: stopped: $aborted"
  } | tee "$dir/readout.txt"
  if grep -q 'FAIL' "$dir/readout.txt"; then
    echo "verdict $scenario $rate/s: FAIL"
    return 1
  fi
  echo "verdict $scenario $rate/s: PASS"
}

verdict=PASS
for scenario in $SCENARIOS; do
  for rate in $(rates_for "$scenario"); do
    if ! run_step "$scenario" "$rate"; then
      verdict=FAIL
      break 2
    fi
  done
done
echo "edge load test: $verdict (results in $OUT)"
[[ "$verdict" == PASS ]]
