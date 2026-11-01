#!/usr/bin/env bash
# Exercise run.sh with a fake k6, node SSH and Prometheus: the share and
# control-plane checks decide the verdict, and the readout names every
# measurement the runbook reads.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(mktemp -d)"
trap 'rm -rf "$ROOT"' EXIT
mkdir -p "$ROOT/bin"

cat >"$ROOT/bin/k6" <<'FAKE'
#!/usr/bin/env bash
echo "k6 $*" >>"$FAKE_LOG"
FAKE
# The fake SSH answers systemd, the cgroup and Prometheus queries on stdin.
cat >"$ROOT/bin/node-ssh" <<'FAKE'
#!/usr/bin/env bash
host=$1 cmd=$2
case "$cmd" in
  *CPUUsageNSec*)
    n=$(cat "$FAKE_DIR/cpu" 2>/dev/null || echo 0); echo $((n + 1)) >"$FAKE_DIR/cpu"
    for _ in 1 2 3; do echo $((n * 1000000000)); done ;;
  *memory.peak*) echo 209715200 ;;
  *api/v1/query*)
    q=$(cat)
    echo "$host $q" >>"$FAKE_LOG"
    case "$q" in
      *transfer_limit_bytes_per_second*) v=37500000 ;;
      max_over_time\(sum\(rate\(s3_gateway_transfer_bytes_total*) v=${FAKE_PEAK:-37400000} ;;
      *pressure_stall*) v=${FAKE_STALL:-0.12} ;;
      *transfer_throttled_seconds_total*) v=${FAKE_WAITED:-42} ;;
      *"by (code)"*) printf '{"data":{"result":[{"metric":{"code":"2xx"},"value":[0,"36000"]},{"metric":{"code":"5xx"},"value":[0,"2"]}]}}'; exit 0 ;;
      *) v=1 ;;
    esac
    printf '{"data":{"result":[{"metric":{},"value":[0,"%s"]}]}}' "$v" ;;
  *) echo "unexpected: $host $cmd" >&2; exit 1 ;;
esac
FAKE
chmod +x "$ROOT/bin/k6" "$ROOT/bin/node-ssh"

run() {
  rm -rf "$ROOT/out" "$ROOT/fake" && mkdir -p "$ROOT/fake"
  : >"$ROOT/log"
  PATH="$ROOT/bin:$PATH" FAKE_LOG="$ROOT/log" FAKE_DIR="$ROOT/fake" \
    NODE=fsn-3 NODE_SSH="$ROOT/bin/node-ssh" OUT="$ROOT/out" SCRAPE_WAIT_S=0 \
    S3_ENDPOINT=https://s3-pilot.example.test S3_BUCKET=b S3_ACCESS_KEY_ID=a S3_SECRET_ACCESS_KEY=s \
    SCENARIOS="seed large-get" "$@" bash "$HERE/run.sh"
}
fail() { echo "FAIL: $*" >&2; exit 1; }

out="$(run env)" || fail "a run within the share failed: $out"
for want in "share: PASS" "cp postgres: cpu stall peak 0.12" "memory peak faas-s3-gatewayd-compute 200 MiB" \
  "cpu faas-s3-gatewayd-compute" "cpu faas-cloudflared-s3" "requests: 2xx=36000 5xx=2" "wakes: p95" "node busy" \
  "share and control-plane checks: PASS"; do
  [[ "$out" == *"$want"* ]] || fail "readout misses \"$want\": $out"
done
grep -q 'k6 run .*SCENARIO=seed' "$ROOT/log" || fail "seed did not run"
grep -q 'k6 run .*SCENARIO=large-get.*VUS=16' "$ROOT/log" || fail "large-get did not run with its readers"
grep -q 'gregale-cp .*job="s3-gatewayd-compute",node="fsn-3"' "$ROOT/log" || fail "Prometheus was not asked about the pilot node's gateway"
[[ -s "$ROOT/out/large-get.txt" ]] || fail "no readout file"

# Past the share, or with control-plane Postgres stalled, the run fails.
if out="$(run env FAKE_PEAK=45000000)"; then fail "a run past the share passed: $out"; fi
[[ "$out" == *"share: FAIL"* ]] || fail "share failure not named: $out"
if out="$(run env FAKE_STALL=0.7)"; then fail "a run with Postgres stalled passed: $out"; fi
[[ "$out" == *"cpu stall peak 0.7"*": FAIL"* ]] || fail "stall failure not named: $out"

# A client too slow to make the limiter wait has not tested the share.
if out="$(run env FAKE_PEAK=20000000 FAKE_WAITED=0)"; then fail "a run below the share passed: $out"; fi
[[ "$out" == *"share not reached"* ]] || fail "unreached share not named: $out"

# Without the pilot node the runner refuses before any request.
if PATH="$ROOT/bin:$PATH" FAKE_LOG="$ROOT/log" S3_ENDPOINT=x S3_BUCKET=b S3_ACCESS_KEY_ID=a S3_SECRET_ACCESS_KEY=s \
  bash "$HERE/run.sh" >/dev/null 2>&1; then
  fail "ran without NODE"
fi

echo "s3-gateway load run_test: OK"
