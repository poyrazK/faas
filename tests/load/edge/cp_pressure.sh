#!/usr/bin/env bash
# Runs on the control plane while a step of the edge load test runs: every
# 5 s, control-plane Postgres CPU pressure (cgroup v2 PSI "some", the series
# FaasPostgresCPUStalled reads) as "avg10_percent total_usec". Argument: the
# number of samples.
set -euo pipefail
f=/sys/fs/cgroup/system.slice/system-postgresql.slice/cpu.pressure
for _ in $(seq "$1"); do
  awk '/^some/ { for (i = 2; i <= NF; i++) { split($i, kv, "="); v[kv[1]] = kv[2] } print v["avg10"], v["total"] }' "$f"
  sleep 5
done
