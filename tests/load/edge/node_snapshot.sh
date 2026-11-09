#!/usr/bin/env bash
# Runs on a compute node; run.sh sends it over NODE_SSH before and after each
# step of the edge load test. Prints one "key value" line per counter it reads.
# Arguments: the edge's metrics port (FAAS_PUBLIC_CONTROL_ADDR) and
# gatewayd-internal's.
set -euo pipefail
edge_port=${1:-9109}
internal_port=${2:-9090}
cg=/sys/fs/cgroup/faas.slice/faas-cp.slice

echo "now $(date +%s.%N)"
for unit in gatewayd-public-edge gatewayd-internal; do
  echo "cpu_usec.$unit $(awk '/^usage_usec/ {print $2}' "$cg/faas-$unit.service/cpu.stat")"
done
read -r _ user nice system idle iowait irq softirq steal _ </proc/stat
echo "proc.total $((user + nice + system + idle + iowait + irq + softirq + steal))"
echo "proc.idle $((idle + iowait))"
# The edge's client-limit refusals, by reason.
curl -fsS --max-time 5 "http://127.0.0.1:$edge_port/metrics" |
  awk '/^gatewayd_public_edge_refused_total\{/ {
    match($0, /reason="[^"]*"/); print "refused." substr($0, RSTART + 8, RLENGTH - 9), $NF }'
# gatewayd-internal: hosts answered without a store read, and database pool
# acquires (each unknown host it does look up costs about two).
curl -fsS --max-time 5 "http://127.0.0.1:$internal_port/metrics" |
  awk '/^gateway_route_lookup_unknown_host_total\{/ {
         match($0, /outcome="[^"]*"/); print "unknown_host." substr($0, RSTART + 9, RLENGTH - 10), $NF }
       /^gatewayd_db_pool_acquires_total/ { acquires += $NF; seen = 1 }
       END { if (seen) print "db_pool.acquires", acquires }'
