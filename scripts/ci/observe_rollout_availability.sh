#!/usr/bin/env bash
# Observe independent public paths while a control-plane rollout runs.
# The API readiness and canary app paths gate only after healthy baselines;
# the telemetry-heavy public status path is diagnostic, never a gate.
# A gated path fails on any failure other than a timeout (non-2xx, refused
# connection) and on more than isolated timeouts (budget in the summary below).
set -euo pipefail

if (( $# < 3 )) || [[ "$2" != "--" ]]; then
  echo "usage: $0 API_READINESS_URL -- COMMAND [ARG ...]" >&2
  exit 2
fi

probe_url="$1"
shift 2
probe_interval_seconds="${ROLLOUT_PROBE_INTERVAL_SECONDS:-0.25}"
baseline_sample_count="${ROLLOUT_BASELINE_SAMPLE_COUNT:-3}"
# Every rollout proves a healthy baseline before it starts: no release may
# rely on stopped writers or a maintenance window (ADR-202610171900).
if ! [[ "$baseline_sample_count" =~ ^[1-9][0-9]*$ ]]; then
  echo "ROLLOUT_BASELINE_SAMPLE_COUNT must be a positive integer, got '$baseline_sample_count'" >&2
  exit 2
fi
# A baseline sample that timed out (curl exit 28, no answer) is retaken, at
# most ROLLOUT_BASELINE_TIMEOUT_BUDGET times per path. The control plane's
# egress to the edge loses connection attempts in bursts: rc.372's rollout
# was refused twice on 2026-11-22 by one timed-out sample of three, the other
# two answering in under 150 ms. The baseline still needs
# ROLLOUT_BASELINE_SAMPLE_COUNT healthy samples, and any other failure (an
# HTTP error, a refused connection) still refuses the rollout.
baseline_timeout_budget="${ROLLOUT_BASELINE_TIMEOUT_BUDGET:-2}"
if ! [[ "$baseline_timeout_budget" =~ ^[0-9]+$ ]]; then
  echo "ROLLOUT_BASELINE_TIMEOUT_BUDGET must be a non-negative integer, got '$baseline_timeout_budget'" >&2
  exit 2
fi
# The final API sample after activation is retaken the same way (below).
final_timeout_retake_budget="${ROLLOUT_FINAL_TIMEOUT_RETAKES:-2}"
if ! [[ "$final_timeout_retake_budget" =~ ^[0-9]+$ ]]; then
  echo "ROLLOUT_FINAL_TIMEOUT_RETAKES must be a non-negative integer, got '$final_timeout_retake_budget'" >&2
  exit 2
fi
# A control plane rebuilt from backups has no API until its first
# activation starts it (docs/runbooks/ControlPlaneStateBackup.md, "Rebuild a
# control plane from backups"). Its baseline cannot be healthy, so that one
# activation is judged on the API becoming ready afterwards, within
# ROLLOUT_FIRST_ACTIVATION_READY_SECONDS, instead of on a baseline.
first_activation="${ROLLOUT_FIRST_ACTIVATION:-false}"
first_ready_seconds="${ROLLOUT_FIRST_ACTIVATION_READY_SECONDS:-300}"
if [[ "$first_activation" != true && "$first_activation" != false ]]; then
  echo "ROLLOUT_FIRST_ACTIVATION must be true or false, got '$first_activation'" >&2
  exit 2
fi
if ! [[ "$first_ready_seconds" =~ ^[1-9][0-9]*$ ]]; then
  echo "ROLLOUT_FIRST_ACTIVATION_READY_SECONDS must be a positive integer, got '$first_ready_seconds'" >&2
  exit 2
fi
if [[ "$first_activation" == true && "${ROLLOUT_REQUIRE_API_BASELINE:-false}" == true ]]; then
  echo "a first activation has no API baseline to require; unset ROLLOUT_REQUIRE_API_BASELINE" >&2
  exit 2
fi
probe_proxy="${ROLLOUT_PROBE_PROXY:-}"
app_url="${ROLLOUT_APP_PROBE_URL:-}"
status_url="${ROLLOUT_STATUS_PROBE_URL:-}"
probe_prefix="${RUNNER_TEMP:-/tmp}/gregale-rollout-availability-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}"
primary_log="${probe_prefix}-api.tsv"
app_log="${probe_prefix}-app.tsv"
status_log="${probe_prefix}-status.tsv"
logs=("$primary_log")
: > "$primary_log"
if [[ -n "$app_url" ]]; then
  logs+=("$app_log")
  : > "$app_log"
fi
if [[ -n "$status_url" ]]; then
  logs+=("$status_log")
  : > "$status_log"
fi

curl_args=(--silent --show-error --location)
if [[ -n "$probe_proxy" ]]; then
  curl_args+=(--proxy "$probe_proxy")
fi

sample() {
  local phase="$1" target="$2" url="$3" sequence="$4" log="$5" timeout="$6"
  local separator='?' result curl_exit error_file error code elapsed connect first_byte
  [[ "$url" == *\?* ]] && separator='&'
  local request_url="${url}${separator}rollout_probe=${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${target}-${sequence}"
  local observed_at
  observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  error_file="$(mktemp)"
  # The connect phase includes the TLS handshake. This observer runs on the
  # control-plane host it measures, which is CPU-saturated during activation;
  # a 1 s connect budget failed rc.263 and rc.264 rollouts on handshakes that
  # finished late on the client (H10-52). The request keeps its total budget.
  if result="$(curl "${curl_args[@]}" \
    --output /dev/null --write-out $'%{http_code}\t%{time_total}\t%{time_connect}\t%{time_starttransfer}' \
    --connect-timeout "$timeout" --max-time "$timeout" \
    --header 'Cache-Control: no-cache' "$request_url" 2>"$error_file")"; then
    curl_exit=0
  else
    curl_exit=$?
  fi
  error="$(tr '\r\n\t' '   ' < "$error_file")"
  error="${error:0:240}"
  rm -f -- "$error_file"
  IFS=$'\t' read -r code elapsed connect first_byte <<< "$result"
  [[ "$code" =~ ^[0-9]{3}$ ]] || code=000
  for field in elapsed connect first_byte; do
    [[ "${!field:-}" =~ ^[0-9]+([.][0-9]+)?$ ]] || printf -v "$field" '0'
  done
  printf '%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n' \
    "$phase" "$target" "$sequence" "$observed_at" "$code" "$elapsed" \
    "$connect" "$first_byte" "$curl_exit" "$error" >> "$log"
}

last_sample_timed_out() {
  tail -n 1 "$1" | awk -F '\t' '{ exit !($9 == 28) }'
}

# baseline_sample TARGET URL LOG TIMEOUT SEQUENCE_VAR RETAKES_VAR: one baseline
# sample, retaken while it timed out and the path's retake budget lasts.
baseline_sample() {
  local target="$1" url="$2" log="$3" timeout="$4" sequence_var="$5" retakes_var="$6"
  printf -v "$sequence_var" '%d' $(( ${!sequence_var} + 1 ))
  sample baseline "$target" "$url" "${!sequence_var}" "$log" "$timeout"
  while last_sample_timed_out "$log" && (( ${!retakes_var} < baseline_timeout_budget )); do
    printf -v "$retakes_var" '%d' $(( ${!retakes_var} + 1 ))
    printf -v "$sequence_var" '%d' $(( ${!sequence_var} + 1 ))
    sleep "$probe_interval_seconds"
    sample baseline "$target" "$url" "${!sequence_var}" "$log" "$timeout"
  done
}

# Each target gets its own baseline. An unhealthy app cannot be blamed on the
# activation, but a healthy API baseline can still detect an API regression.
# shellcheck disable=SC2034 # baseline_sample updates these through printf -v.
api_baseline_sequence=0 api_baseline_retakes=0 app_baseline_sequence=0 app_baseline_retakes=0
for (( i=1; i<=baseline_sample_count; i++ )); do
  baseline_sample api "$probe_url" "$primary_log" 3 api_baseline_sequence api_baseline_retakes
  if [[ -n "$app_url" ]]; then
    baseline_sample app "$app_url" "$app_log" 8 app_baseline_sequence app_baseline_retakes
  fi
  sleep "$probe_interval_seconds"
done
if [[ -n "$status_url" ]]; then
  sample baseline status "$status_url" 1 "$status_log" 3
fi

if [[ "${ROLLOUT_REQUIRE_API_BASELINE:-false}" == true ]]; then
  # Healthy: the required healthy samples, timeouts within the retake
  # budget, and no other failure.
  if ! awk -F '\t' -v need="$baseline_sample_count" -v budget="$baseline_timeout_budget" '
      $9 == 28 { timeouts++; next }
      $9 == 0 && $5 >= 200 && $5 < 300 { healthy++; next }
      { other++ }
      END { exit !(healthy >= need && timeouts <= budget && other == 0) }' "$primary_log"; then
    echo "API readiness baseline failed; activation was not started" >&2
    exit 1
  fi
fi

"$@" &
command_pid=$!
secondary_pids=()

# shellcheck disable=SC2329 # Invoked by the INT/TERM trap below.
cleanup() {
  kill -TERM "$command_pid" 2>/dev/null || true
  for pid in "${secondary_pids[@]:-}"; do
    [[ -n "$pid" ]] || continue
    kill -TERM "$pid" 2>/dev/null || true
  done
  wait "$command_pid" 2>/dev/null || true
  for pid in "${secondary_pids[@]:-}"; do
    [[ -n "$pid" ]] || continue
    wait "$pid" 2>/dev/null || true
  done
}
trap cleanup INT TERM

sample_number=0
while kill -0 "$command_pid" 2>/dev/null; do
  sample_number=$((sample_number + 1))
  sample rollout api "$probe_url" "$sample_number" "$primary_log" 3
  # Slow app/status requests must not pause the high-frequency API observer.
  # The app is a separate customer-path gate; status is sampled sparingly for
  # diagnosis so the probe itself does not hammer Prometheus during activation.
  if [[ -n "$app_url" ]] && (( sample_number % 20 == 1 )); then
    sample rollout app "$app_url" "$sample_number" "$app_log" 8 &
    secondary_pids+=("$!")
  fi
  if [[ -n "$status_url" ]] && (( sample_number % 60 == 1 )); then
    sample rollout status "$status_url" "$sample_number" "$status_log" 3 &
    secondary_pids+=("$!")
  fi
  sleep "$probe_interval_seconds"
done

command_status=0
wait "$command_pid" || command_status=$?
sample_number=$((sample_number + 1))
sample rollout api "$probe_url" "$sample_number" "$primary_log" 3
# A final sample that timed out is retaken, at most
# ROLLOUT_FINAL_TIMEOUT_RETAKES times, as a baseline sample is: the control
# plane's egress to the edge loses connection attempts in bursts, and the
# unit lane's first production run (2026-11-23) failed on a timed-out last
# sample, the hand-off having passed its checks and the app path healthy.
# The judge below still counts every retake against the timeout budget and
# the consecutive-failure streak, and any answer other than a timeout fails.
final_timeout_retakes=0
while last_sample_timed_out "$primary_log" && (( final_timeout_retakes < final_timeout_retake_budget )); do
  final_timeout_retakes=$((final_timeout_retakes + 1))
  sleep "$probe_interval_seconds"
  sample_number=$((sample_number + 1))
  sample rollout api "$probe_url" "$sample_number" "$primary_log" 3
done
if [[ "$first_activation" == true ]] && (( command_status == 0 )); then
  ready_deadline=$((SECONDS + first_ready_seconds))
  until tail -n 1 "$primary_log" | awk -F '\t' '{ exit !($9 == 0 && $5 >= 200 && $5 < 300) }' \
    || (( SECONDS >= ready_deadline )); do
    sleep "$probe_interval_seconds"
    sample_number=$((sample_number + 1))
    sample rollout api "$probe_url" "$sample_number" "$primary_log" 3
  done
fi
if [[ -n "$app_url" ]]; then
  sample rollout app "$app_url" "$sample_number" "$app_log" 8
fi
if [[ -n "$status_url" ]]; then
  sample rollout status "$status_url" "$sample_number" "$status_log" 3
fi
for pid in "${secondary_pids[@]:-}"; do
  [[ -n "$pid" ]] || continue
  wait "$pid" || true
done
trap - INT TERM

gate_status=0
if summary="$(ROLLOUT_FIRST_ACTIVATION="$first_activation" ROLLOUT_BASELINE_SAMPLE_COUNT="$baseline_sample_count" \
  ROLLOUT_BASELINE_TIMEOUT_BUDGET="$baseline_timeout_budget" python3 - "$probe_url" "$app_url" "${logs[@]}" <<'PY'
import collections
import math
import os
import sys

urls = {"api": sys.argv[1], "app": sys.argv[2]}
samples = collections.defaultdict(list)
for path in sys.argv[3:]:
    with open(path, encoding="utf-8") as stream:
        for line in stream:
            phase, target, sequence, observed_at, status, elapsed, connect, first_byte, curl_exit, error = line.rstrip("\n").split("\t", 9)
            samples[target].append((phase, int(sequence), observed_at, int(status), float(elapsed), float(connect), float(first_byte), int(curl_exit), error))

# A timed-out sample (curl exit 28) is latency on the control plane that runs
# this observer, not an outage: rc.304, rc.305 and rc.307 each failed
# activation on one or two 3 s timeouts while the bundle unpacked, with every
# later sample healthy. Up to TIMEOUT_BUDGET timeouts are tolerated when no
# run exceeds TIMEOUT_STREAK samples and the final sample is healthy. Any other
# failure (an HTTP error, a refused connection) still fails the gate, so a
# broken handoff is caught.
CURL_TIMEOUT = 28
TIMEOUT_BUDGET = 3
TIMEOUT_STREAK = 2
# The baseline retakes a timed-out sample within its own budget (see the
# shell above); its health uses the same rule here.
BASELINE_SAMPLES = int(os.environ.get("ROLLOUT_BASELINE_SAMPLE_COUNT", "1"))
BASELINE_TIMEOUT_BUDGET = int(os.environ.get("ROLLOUT_BASELINE_TIMEOUT_BUDGET", "0"))

gate_failed = False
first_activation = os.environ.get("ROLLOUT_FIRST_ACTIVATION") == "true"
rows = []
details = []
warnings = []
tolerated = []
for target in ("api", "app", "status"):
    if target not in samples:
        continue
    baseline = [sample for sample in samples[target] if sample[0] == "baseline"]
    rollout = [sample for sample in samples[target] if sample[0] == "rollout"]
    good = lambda sample: sample[7] == 0 and 200 <= sample[3] < 300
    baseline_successful = [sample for sample in baseline if good(sample)]
    successful = [sample for sample in rollout if good(sample)]
    failed = [sample for sample in rollout if not good(sample)]
    baseline_timeouts = [sample for sample in baseline if not good(sample) and sample[7] == CURL_TIMEOUT]
    baseline_other = [sample for sample in baseline if not good(sample) and sample[7] != CURL_TIMEOUT]
    # Status is sampled once and never retaken.
    baseline_needed = 1 if target == "status" else BASELINE_SAMPLES
    baseline_ready = (len(baseline_successful) >= baseline_needed and not baseline_other
                      and len(baseline_timeouts) <= (0 if target == "status" else BASELINE_TIMEOUT_BUDGET))
    if baseline_ready and baseline_timeouts:
        tolerated.append(f"{target} baseline retook {len(baseline_timeouts)} timed-out sample(s) (budget {BASELINE_TIMEOUT_BUDGET}); {len(baseline_successful)} healthy.")
    judged_as_first = first_activation and target == "api"
    if judged_as_first:
        # The rebuilt control plane had no API before this activation; the
        # gate is that it answers ready by the end of the readiness wait.
        final = sorted(rollout, key=lambda sample: sample[1])[-1:]
        if final and good(final[0]):
            tolerated.append(f"api: first activation of a rebuilt control plane; the API answered ready at {final[0][2]} UTC.")
        else:
            gate_failed = True
            details.append("api: gate failed: the API did not answer ready after the first activation of a rebuilt control plane.")
    if target != "status" and baseline_ready and failed and not judged_as_first:
        ordered = sorted(rollout, key=lambda sample: sample[1])
        answered = [sample for sample in failed if sample[7] != CURL_TIMEOUT]
        streak = longest = 0
        for sample in ordered:
            streak = 0 if good(sample) else streak + 1
            longest = max(longest, streak)
        reasons = []
        if answered:
            reasons.append(f"{len(answered)} failure(s) other than a timeout")
        if len(failed) > TIMEOUT_BUDGET:
            reasons.append(f"{len(failed)} timed-out samples (budget {TIMEOUT_BUDGET})")
        if longest > TIMEOUT_STREAK:
            reasons.append(f"{longest} consecutive failed samples (budget {TIMEOUT_STREAK})")
        if not good(ordered[-1]):
            reasons.append("the final sample failed")
        if reasons:
            gate_failed = True
            details.append(f"{target}: gate failed: {'; '.join(reasons)}.")
        else:
            tolerated.append(f"{target} tolerated {len(failed)} isolated timed-out sample(s) of {len(rollout)} (budget {TIMEOUT_BUDGET}, at most {TIMEOUT_STREAK} in a row, final sample healthy).")
    maximum_latency_ms = math.ceil(1000 * max((sample[4] for sample in rollout), default=0))
    rows.append(f"| {target} | {len(baseline_successful)}/{len(baseline)} | {len(rollout)} | {len(successful)} | {len(failed)} | {maximum_latency_ms} ms |")
    if not baseline_ready and not judged_as_first:
        details.append(f"{target}: rollout attribution is **inconclusive** because its baseline was not healthy.")
        if target != "status":
            # A gate without a healthy baseline guards nothing; say so loudly
            # so a vanished canary app is noticed, not read as a pass.
            baseline_counts = ", ".join(f"HTTP {status:03d}: {count}" for status, count in sorted(collections.Counter(sample[3] for sample in baseline).items()))
            hint = " ROLLOUT_APP_PROBE_URL names the canary app." if target == "app" else ""
            warnings.append(f"{target} gate gave this activation no signal: its baseline was {len(baseline_successful)}/{len(baseline)} healthy ({baseline_counts or 'no samples'}). Check that {urls[target]} still serves.{hint}")
    status_counts = ", ".join(f"{status:03d}: {count}" for status, count in sorted(collections.Counter(sample[3] for sample in rollout).items()))
    exit_counts = ", ".join(f"{code}: {count}" for code, count in sorted(collections.Counter(sample[7] for sample in rollout).items()))
    details.append(f"{target}: HTTP status counts: {status_counts or 'none'}; curl exit counts: {exit_counts or 'none'}.")
    if failed:
        details.append(f"{target}: failed samples ran from {failed[0][2]} through {failed[-1][2]} UTC.")
        for sample in failed[:5]:
            details.append(f"- {sample[2]} HTTP {sample[3]:03d}, curl exit {sample[7]}, total {sample[4]:.3f}s, connect {sample[5]:.3f}s, first byte {sample[6]:.3f}s: {sample[8] or 'no curl error'}")
        if len(failed) > 5:
            details.append(f"- ... {len(failed) - 5} additional failures in the TSV artifact")
    details.append("")

print("### Control-plane activation public-path observations")
print()
for warning in warnings:
    print(f"> [!WARNING]\n> {warning}\n")
    escaped = warning.replace("%", "%25")
    print(f"::warning title=Rollout gate baseline unhealthy::{escaped}", file=sys.stderr)
for note in tolerated:
    print(f"> [!NOTE]\n> {note}\n")
    escaped = note.replace("%", "%25")
    print(f"::warning title=Rollout gate tolerated timeouts::{escaped}", file=sys.stderr)
print("API readiness and canary app are independent gates after healthy baselines. Public status is diagnostic only. A gate tolerates isolated timeouts, never any other failure.")
print()
print("| Path | Baseline | Rollout samples | Successful | Failed | Max latency |")
print("| --- | ---: | ---: | ---: | ---: | ---: |")
for row in rows:
    print(row)
print()
for detail in details:
    print(detail)

if gate_failed:
    sys.exit(1)
PY
)"; then
  gate_status=0
else
  gate_status=$?
fi

printf '%s\n' "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf '%s\n' "$summary" >> "$GITHUB_STEP_SUMMARY"
fi

if (( command_status != 0 )); then
  exit "$command_status"
fi
if (( gate_status != 0 )); then
  echo "public readiness or canary app failed after a healthy pre-rollout baseline" >&2
  exit 1
fi

exit 0
