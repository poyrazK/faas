#!/usr/bin/env bash
# Exercise run.sh with a fake k6, curl, node SSH and control plane: a clean
# run passes and names every measurement the runbook reads; a bad answer, a
# refusal, a stalled Postgres or an unreachable rate fails and ends the run;
# unsafe settings are refused before any request.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

# The fake k6 records its arguments and writes a summary: RATE*70 requests,
# all 404 unless FAKE_503 is set.
cat >"$ROOT/bin/k6" <<'FAKE'
#!/usr/bin/env bash
echo "k6 $*" >>"$FAKE_LOG"
out="" rate=0
while (($#)); do
  case $1 in
    --summary-export) out=$2; shift ;;
    -e) case $2 in RATE=*) rate=${2#RATE=} ;; esac; shift ;;
  esac
  shift
done
sleep "${FAKE_K6_SLEEP:-0}"
reqs=$((rate * 70)) bad=${FAKE_503:-0}
cat >"$out" <<JSON
{"metrics":{"http_reqs":{"count":$reqs,"rate":1},"s404":{"count":$((reqs - bad))},"bad":{"count":$bad},
 "s503":{"count":$bad},"dropped_iterations":{"count":0},
 "http_req_duration":{"p(50)":165.2,"p(99)":${FAKE_P99:-240.7}}}}
JSON
FAKE
cat >"$ROOT/bin/curl" <<'FAKE'
#!/usr/bin/env bash
echo "curl $*" >>"$FAKE_LOG"
printf '%s' "${FAKE_MISS_CODE:-404}"
FAKE
# The fake SSH runs nothing: it tells the node snapshot from the control-plane
# sampler by the script on stdin. Each node snapshot advances that node's
# counters by one step: 70 s, 0.2 cores of edge (0.1 in the idle baseline,
# the first pair), 50% busy.
cat >"$ROOT/bin/node-ssh" <<'FAKE'
#!/usr/bin/env bash
host=$1 cmd=$2 script=$(cat)
echo "ssh $host $cmd" >>"$FAKE_LOG"
case "$script" in
  *system-postgresql.slice*)
    # One sample is the calm check before a step; more is the step's watch.
    if [[ "$cmd" == "bash -s -- 1" ]]; then echo "${FAKE_CALM_AVG10:-4.2} 1000000"; exit 0; fi
    echo "${FAKE_AVG10:-4.2} 1000000"
    echo "${FAKE_AVG10:-4.2} $((1000000 + ${FAKE_STALL_USEC:-1000000}))" ;;
  *gatewayd_public_edge_refused_total*)
    n=$(cat "$FAKE_DIR/$host" 2>/dev/null || echo 0); echo $((n + 1)) >"$FAKE_DIR/$host"
    echo "WARNING: an IAP notice on stdout"
    echo "now $((n * 70)).000"
    echo "cpu_usec.gatewayd-public-edge $((n == 0 ? 0 : 7000000 + (n - 1) * 14000000))"
    echo "cpu_usec.gatewayd-internal $((n * 21000000))"
    echo "proc.total $((n * 2800))"
    echo "proc.idle $((n * 1400))"
    echo "refused.request_rate $((n * ${FAKE_REFUSED:-0}))"
    echo "refused.connection_rate 0"
    echo "unknown_host.remembered_miss $((n * 3000))"
    echo "unknown_host.shed 0"
    echo "db_pool.acquires $((n * 4))" ;;
  *) echo "unexpected script for $host" >&2; exit 1 ;;
esac
FAKE
chmod +x "$ROOT/bin/"*

run() {
  rm -rf "$ROOT/out" "$ROOT/fake" && mkdir -p "$ROOT/fake"
  : >"$ROOT/log"
  PATH="$ROOT/bin:$PATH" FAKE_LOG="$ROOT/log" FAKE_DIR="$ROOT/fake" \
    NODES="node-a=192.0.2.10 node-b=192.0.2.11" DOMAIN=apps.example.test NODE_SSH="$ROOT/bin/node-ssh" \
    OUT="$ROOT/out" BASELINE_S=0 AVOID_CRON_WAVES=0 POLL_S=1 RUN_ID=t1 \
    SCENARIOS=miss MISS_RATES="50 100" "$@" bash "$HERE/run.sh"
}
fail() { echo "FAIL: $*" >&2; exit 1; }

out="$(run env 2>&1)" || fail "a clean run failed: $out"
[[ -z "${SHOW:-}" ]] || echo "$out"
for want in "verdict miss 50/s: PASS" "verdict miss 100/s: PASS" "edge load test: PASS" \
  "node node-a (192.0.2.10): 3500 requests of 3300: 404 3500, bad 0" "p50 165 ms, p99 240 ms" \
  "public-edge 0.20 cores (net 2000 us/request)" "internal 0.30 cores (net 0 us/request)" "node busy 50%" "refused: none" \
  "remembered_miss +3000, shed +0" "db pool acquires +4" "cp postgres: cpu stall 20.0% of the step"; do
  [[ "$out" == *"$want"* ]] || fail "readout misses \"$want\": $out"
done
grep -q 'k6 run .*SCENARIO=miss .*TARGET_IP=192.0.2.10 .*MISS_HOST=lt-edge-none.apps.example.test .*RATE=50 ' "$ROOT/log" ||
  fail "node-a was not driven at its entry address"
grep -q 'k6 run .*TARGET_IP=192.0.2.11 .*RUN_ID=t1n2 .*RATE=100 ' "$ROOT/log" || fail "node-b's second step did not run"
grep -q 'curl .*--resolve lt-edge-none.apps.example.test:443:192.0.2.11' "$ROOT/log" || fail "the miss host was not checked at node-b"
grep -q 'ssh gregale-cp bash -s -- 18' "$ROOT/log" || fail "control-plane Postgres was not sampled over the step"

# A bad answer fails the step and ends the run before the next, harder step.
if out="$(run env FAKE_503=3 2>&1)"; then fail "a run with 503s passed: $out"; fi
[[ "$out" == *"FAIL: answers other than 404"* && "$out" == *"verdict miss 50/s: FAIL"* ]] || fail "503s not named: $out"
if grep -q 'RATE=100 ' "$ROOT/log"; then fail "the run went on after a failed step"; fi

# So do a refusal at the edge, a slow p99, a stalled Postgres and a short run.
if out="$(run env FAKE_REFUSED=7 2>&1)"; then fail "a run with refusals passed: $out"; fi
[[ "$out" == *"refused: request_rate+7"* && "$out" == *"FAIL: the edge refused"* ]] || fail "refusal not named: $out"
if out="$(run env FAKE_P99=2400 2>&1)"; then fail "a run with a slow p99 passed: $out"; fi
[[ "$out" == *"FAIL: p99 2400 ms above 1500"* ]] || fail "slow p99 not named: $out"
if out="$(run env FAKE_STALL_USEC=4000000 2>&1)"; then fail "a run with Postgres stalled passed: $out"; fi
[[ "$out" == *"cpu stall 80.0% of the step"* && "$out" == *"FAIL: Postgres stalled"* ]] || fail "stall not named: $out"
if out="$(run env DURATION_S=120 2>&1)"; then fail "a run short of its rate passed: $out"; fi
[[ "$out" == *"the client, not the edge, set the rate"* ]] || fail "short run not named: $out"

# Pressure above the abort line stops k6 mid-step.
start=$(date +%s)
if out="$(run env FAKE_AVG10=73.5 FAKE_K6_SLEEP=30 2>&1)"; then fail "a run with Postgres pressure passed: $out"; fi
(($(date +%s) - start < 20)) || fail "k6 was not stopped on Postgres pressure"
[[ "$out" == *"stopped: control-plane Postgres CPU pressure avg10 73.5% above 50%"* ]] || fail "abort not named: $out"

# A control plane that never calms starts no step.
if out="$(run env FAKE_CALM_AVG10=40 CP_CALM_TRIES=2 CP_CALM_WAIT_S=0 2>&1)"; then fail "a run on a busy control plane passed: $out"; fi
[[ "$out" == *"waiting for control-plane Postgres to calm: CPU pressure avg10 40% above 15%"* &&
  "$out" == *"no step started"* ]] || fail "busy control plane not named: $out"
if grep -q '^k6 ' "$ROOT/log"; then fail "k6 ran on a busy control plane"; fi

# Unsafe or impossible settings are refused before any request.
if out="$(run env MISS_RATES=600 2>&1)"; then fail "a rate past the per-address limit ran: $out"; fi
[[ "$out" == *"outside 1..450"* ]] || fail "rate cap not named: $out"
if out="$(run env SCENARIOS=new-host NEW_HOST_RATES=30 2>&1)"; then fail "a new-host rate past the budget ran: $out"; fi
if out="$(run env FAKE_MISS_CODE=200 2>&1)"; then fail "a miss host that names an app was used: $out"; fi
[[ "$out" == *"must name no app"* ]] || fail "miss host check not named: $out"
if grep -q '^k6 ' "$ROOT/log"; then fail "k6 ran after a refusal"; fi
if PATH="$ROOT/bin:$PATH" DOMAIN=x bash "$HERE/run.sh" >/dev/null 2>&1; then fail "ran without NODES"; fi

echo "edge load run_test: OK"
