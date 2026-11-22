#!/usr/bin/env bash
# Hermetic tests for observe_rollout_availability.sh: curl is stubbed, so no
# network is needed. The API answers 503 until STUB_API_READY_AFTER requests,
# then 200. The API requests listed in STUB_API_TIMEOUTS (comma-separated
# request numbers) time out instead, as curl exit 28 with no answer.
set -euo pipefail
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
observer="$repo_root/scripts/ci/observe_rollout_availability.sh"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }
stub="$test_root/stub"
mkdir -p "$stub"
cat > "$stub/curl" <<'STUB'
#!/usr/bin/env bash
url="${*: -1}"
if [[ "$url" == *readyz* ]]; then
  n=$(( $(cat "$STUB_COUNT" 2>/dev/null || echo 0) + 1 ))
  echo "$n" > "$STUB_COUNT"
  if [[ ",${STUB_API_TIMEOUTS:-}," == *",$n,"* ]]; then
    printf '000\t3.001\t0.000\t0.000'
    echo "curl: (28) Connection timed out after 3001 milliseconds" >&2
    exit 28
  fi
  if (( n > STUB_API_READY_AFTER )); then code=200; else code=503; fi
else
  code=200
fi
printf '%s\t0.010\t0.001\t0.005' "$code"
STUB
chmod +x "$stub/curl"

run() { # run NAME READY_AFTER [ENV=VALUE ...]: prints the observer's exit status
  local name="$1" ready_after="$2"
  shift 2
  : > "$test_root/$name.count"
  env PATH="$stub:$PATH" RUNNER_TEMP="$test_root" GITHUB_RUN_ID="$name" \
    STUB_COUNT="$test_root/$name.count" STUB_API_READY_AFTER="$ready_after" \
    ROLLOUT_PROBE_INTERVAL_SECONDS=0.02 ROLLOUT_BASELINE_SAMPLE_COUNT=2 "$@" \
    bash "$observer" https://api.example/readyz -- bash -c 'sleep 0.2' > "$test_root/$name.out" 2>&1 \
    && echo 0 || echo $?
}

# 1. A first activation: no API before it, ready a few samples after the
#    activation command returns. Passes without a baseline.
[[ "$(run first-ready 30 ROLLOUT_FIRST_ACTIVATION=true)" == 0 ]] \
  || fail "a first activation whose API became ready failed: $(cat "$test_root/first-ready.out")"
grep -q 'first activation of a rebuilt control plane; the API answered ready' "$test_root/first-ready.out" \
  || fail "the first activation was not reported: $(cat "$test_root/first-ready.out")"

# 2. A first activation whose API never answers ready within the wait fails.
[[ "$(run first-never 100000 ROLLOUT_FIRST_ACTIVATION=true ROLLOUT_FIRST_ACTIVATION_READY_SECONDS=1)" == 1 ]] \
  || fail "a first activation whose API never became ready passed: $(cat "$test_root/first-never.out")"
grep -q 'did not answer ready after the first activation' "$test_root/first-never.out" \
  || fail "the failed first activation was not explained"

# 3. A first activation has no baseline to require.
[[ "$(run first-baseline 0 ROLLOUT_FIRST_ACTIVATION=true ROLLOUT_REQUIRE_API_BASELINE=true)" == 2 ]] \
  || fail "a first activation that also required an API baseline was accepted"
[[ "$(run bad-flag 0 ROLLOUT_FIRST_ACTIVATION=yes)" == 2 ]] || fail "ROLLOUT_FIRST_ACTIVATION=yes was accepted"

# 4. An ordinary activation still requires a healthy baseline, and passes with one.
[[ "$(run no-baseline 100000 ROLLOUT_REQUIRE_API_BASELINE=true)" == 1 ]] \
  || fail "an activation started without a healthy API baseline"
grep -q 'API readiness baseline failed; activation was not started' "$test_root/no-baseline.out" \
  || fail "the refused baseline was not explained"
[[ "$(run healthy 0 ROLLOUT_REQUIRE_API_BASELINE=true)" == 0 ]] \
  || fail "a healthy ordinary activation failed: $(cat "$test_root/healthy.out")"

# 5. One timed-out baseline sample is retaken: the rollout starts, and the
#    summary says so (rc.372 was refused twice on exactly this).
[[ "$(run baseline-timeout 0 ROLLOUT_REQUIRE_API_BASELINE=true STUB_API_TIMEOUTS=1)" == 0 ]] \
  || fail "a baseline with one timed-out sample refused the rollout: $(cat "$test_root/baseline-timeout.out")"
grep -q 'api baseline retook 1 timed-out sample(s)' "$test_root/baseline-timeout.out" \
  || fail "the retaken baseline sample was not reported: $(cat "$test_root/baseline-timeout.out")"
grep -q 'inconclusive' "$test_root/baseline-timeout.out" \
  && fail "a retaken baseline timeout left the rollout gate without a baseline"

# 6. Timeouts past the retake budget still refuse the rollout.
[[ "$(run baseline-timeouts 0 ROLLOUT_REQUIRE_API_BASELINE=true STUB_API_TIMEOUTS=1,2,3)" == 1 ]] \
  || fail "a baseline with three timed-out samples (budget 2) started the rollout"
grep -q 'API readiness baseline failed; activation was not started' "$test_root/baseline-timeouts.out" \
  || fail "the refused baseline was not explained"
[[ "$(run baseline-no-retake 0 ROLLOUT_REQUIRE_API_BASELINE=true ROLLOUT_BASELINE_TIMEOUT_BUDGET=0 STUB_API_TIMEOUTS=1)" == 1 ]] \
  || fail "a timed-out baseline sample was retaken with a zero budget"
[[ "$(run bad-budget 0 ROLLOUT_BASELINE_TIMEOUT_BUDGET=-1)" == 2 ]] || fail "ROLLOUT_BASELINE_TIMEOUT_BUDGET=-1 was accepted"

echo "observe_rollout_availability tests passed"
