#!/usr/bin/env bash
# ADR-20261031122757 step 5: run the one-node load test of the S3 gateway on
# a compute node and print its pass/fail readout
# (docs/runbooks/S3GatewayComputeLoadTest.md).
#
# For each scenario it records the window, the CPU the node's gateway,
# connector and gatewayd-internal used in it (systemd CPUUsageNSec), the
# gateway's peak memory, and then asks the control plane's Prometheus:
#   - share:   the gateway's peak 1-minute transfer rate against its limit;
#   - wakes:   gatewayd-internal's wake p95 on the node, against the same
#              length of time just before the test (judged by eye: wakes are
#              few, so "within noise" is a reading, not a threshold);
#   - CP:      control-plane Postgres CPU stall against FaasPostgresCPUStalled;
#   - errors:  the gateway's requests by status class.
#
# Needs k6, jq, the S3_* variables s3_gateway.js reads, NODE (the pilot node's
# compute_nodes name) and NODE_SSH, which runs a command on a host with stdin
# forwarded: NODE_SSH <host> <command>.
set -euo pipefail

: "${NODE:?set NODE to the pilot compute node}"
: "${S3_ENDPOINT:?set S3_ENDPOINT to https://<pilot host>}"
: "${S3_BUCKET:?set S3_BUCKET}"
: "${S3_ACCESS_KEY_ID:?set S3_ACCESS_KEY_ID}"
: "${S3_SECRET_ACCESS_KEY:?set S3_SECRET_ACCESS_KEY}"
NODE_SSH=${NODE_SSH:-$HOME/gregale-e2e/bin/gpssh}
CP=${CP:-gregale-cp}
PROMETHEUS=${PROMETHEUS:-http://127.0.0.1:9095}
SCENARIOS=${SCENARIOS:-seed small-get large-get put}
DURATION_S=${DURATION_S:-180}
SMALL_GET_RATE=${SMALL_GET_RATE:-200}
PUT_RATE=${PUT_RATE:-20}
LARGE_GET_VUS=${LARGE_GET_VUS:-16}
OUT=${OUT:-results-$(date -u +%Y%m%dT%H%M%SZ)}
K6=${K6:-k6}
SCRAPE_WAIT_S=${SCRAPE_WAIT_S:-30}
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UNITS="faas-s3-gatewayd-compute faas-cloudflared-s3 faas-gatewayd-internal"
mkdir -p "$OUT"

# prom <promql> <unix time>: the first sample's value, or NaN.
prom() {
  printf '%s' "$1" | "$NODE_SSH" "$CP" \
    "curl -fsS --get --data-urlencode query@- --data-urlencode time=$2 $PROMETHEUS/api/v1/query" |
    jq -r '.data.result[0].value[1] // "NaN"'
}
# prom_by <promql> <unix time> <label>: "label=value" per series.
prom_by() {
  printf '%s' "$1" | "$NODE_SSH" "$CP" \
    "curl -fsS --get --data-urlencode query@- --data-urlencode time=$2 $PROMETHEUS/api/v1/query" |
    jq -r --arg l "$3" '.data.result[] | "\(.metric[$l] // "?")=\(.value[1] | tonumber | floor)"' | tr '\n' ' '
}
# cpu_ns: CPUUsageNSec of each unit, one per line, in UNITS order.
cpu_ns() {
  "$NODE_SSH" "$NODE" "systemctl show -p CPUUsageNSec --value $UNITS" </dev/null
}
gateway_memory_peak() {
  # shellcheck disable=SC2016 # expanded on the node
  "$NODE_SSH" "$NODE" 'cat "/sys/fs/cgroup$(systemctl show -p ControlGroup --value faas-s3-gatewayd-compute)/memory.peak"' </dev/null
}

sel="job=\"s3-gatewayd-compute\",node=\"$NODE\""
verdict=PASS
for scenario in $SCENARIOS; do
  args=(-e "SCENARIO=$scenario" -e "DURATION=${DURATION_S}s")
  case "$scenario" in
    small-get) args+=(-e "RATE=$SMALL_GET_RATE") ;;
    put) args+=(-e "RATE=$PUT_RATE") ;;
    large-get) args+=(-e "VUS=$LARGE_GET_VUS") ;;
  esac
  read -r -a before <<<"$(cpu_ns | tr '\n' ' ')"
  start=$(date +%s)
  "$K6" run --quiet --summary-export "$OUT/$scenario.json" "${args[@]}" "$HERE/s3_gateway.js" >"$OUT/$scenario.k6.txt" 2>&1 \
    || echo "$scenario: k6 exited non-zero; see $OUT/$scenario.k6.txt"
  end=$(date +%s)
  read -r -a after <<<"$(cpu_ns | tr '\n' ' ')"
  [[ "$scenario" == seed ]] && { echo "seed: fixtures written ($((end - start)) s)"; continue; }
  w=$((end - start))
  ((w > 0)) || w=1
  # Let the last scrape land before reading the window.
  sleep "$SCRAPE_WAIT_S"
  {
    echo "== $scenario: $(date -u -r "$start" +%FT%TZ 2>/dev/null || date -u -d "@$start" +%FT%TZ) for ${w}s"
    i=0
    for unit in $UNITS; do
      awk -v a="${after[$i]}" -v b="${before[$i]}" -v w="$w" -v u="$unit" 'BEGIN { printf "cpu %-26s %.2f cores\n", u, (a - b) / 1e9 / w }'
      i=$((i + 1))
    done
    echo "memory peak faas-s3-gatewayd-compute $(( $(gateway_memory_peak) / 1048576 )) MiB (unit MemoryMax 512)"
    limit=$(prom "max(s3_gateway_transfer_limit_bytes_per_second{$sel})" "$end")
    peak=$(prom "max_over_time(sum(rate(s3_gateway_transfer_bytes_total{$sel}[1m]))[${w}s:15s])" "$end")
    mean=$(prom "sum(increase(s3_gateway_transfer_bytes_total{$sel}[${w}s])) / $w" "$end")
    waited=$(prom "sum(increase(s3_gateway_transfer_throttled_seconds_total{$sel}[${w}s]))" "$end")
    echo "transfer limit $limit B/s, peak 1m $peak B/s, mean $mean B/s, waited ${waited}s"
    # The limiter lets a 1 MiB burst through; 5% covers it and scrape jitter.
    share=$(awk -v p="$peak" -v l="$limit" 'BEGIN { print (l > 0 && p <= l * 1.05) ? "PASS" : "FAIL" }')
    # large-get must reach the share to test it: a client too slow to make
    # the limiter wait proves nothing.
    if [[ "$scenario" == large-get && "$share" == PASS ]] && ! awk -v s="$waited" 'BEGIN { exit !(s > 0) }'; then
      share="FAIL (share not reached: the limiter never waited; use a faster client or more VUS)"
    fi
    echo "share: $share"
    echo "requests: $(prom_by "sum by (code) (increase(s3_gateway_ops_total{$sel,op=\"s3_request\"}[${w}s]))" "$end" code)"
    wake="histogram_quantile(0.95, sum by (le) (increase(gateway_wake_latency_seconds_bucket{job=\"gatewayd-internal\",node=\"$NODE\"}[${w}s])))"
    wakes="sum(increase(gateway_wake_latency_seconds_count{job=\"gatewayd-internal\",node=\"$NODE\"}[${w}s]))"
    echo "wakes: p95 $(prom "$wake" "$end") s over $(prom "$wakes" "$end") wakes; before the test p95 $(prom "$wake" "$start") s over $(prom "$wakes" "$start")"
    stall=$(prom "max_over_time(rate(faas_cgroup_pressure_stall_seconds_total{cgroup=\"system.slice/system-postgresql.slice\",resource=\"cpu\",kind=\"some\"}[10m])[${w}s:30s])" "$end")
    cp=$(awk -v s="$stall" 'BEGIN { print (s != "NaN" && s <= 0.5) ? "PASS" : "FAIL" }')
    echo "cp postgres: cpu stall peak $stall (FaasPostgresCPUStalled above 0.5): $cp"
    node_busy=$(prom "1 - avg(rate(node_cpu_seconds_total{job=\"node-compute\",node=\"$NODE\",mode=\"idle\"}[${w}s]))" "$end")
    echo "node busy $node_busy"
    [[ "$share" == PASS && "$cp" == PASS ]] || echo "VERDICT-FAIL"
  } | tee "$OUT/$scenario.txt"
  grep -q VERDICT-FAIL "$OUT/$scenario.txt" && verdict=FAIL
done
echo "share and control-plane checks: $verdict (wake p95 and the sizing lines are read by eye; results in $OUT)"
[[ "$verdict" == PASS ]]
