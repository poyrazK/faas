#!/usr/bin/env bash
# Observe independent public paths while a control-plane rollout runs.
# The API readiness and canary app paths gate only after healthy baselines;
# the telemetry-heavy public status path is diagnostic, never a gate.
set -euo pipefail

if (( $# < 3 )) || [[ "$2" != "--" ]]; then
  echo "usage: $0 API_READINESS_URL -- COMMAND [ARG ...]" >&2
  exit 2
fi

probe_url="$1"
shift 2
probe_interval_seconds="${ROLLOUT_PROBE_INTERVAL_SECONDS:-0.25}"
baseline_sample_count="${ROLLOUT_BASELINE_SAMPLE_COUNT:-3}"
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
  if result="$(curl "${curl_args[@]}" \
    --output /dev/null --write-out $'%{http_code}\t%{time_total}\t%{time_connect}\t%{time_starttransfer}' \
    --connect-timeout 1 --max-time "$timeout" \
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

# Each target gets its own baseline. An unhealthy app cannot be blamed on the
# activation, but a healthy API baseline can still detect an API regression.
for (( i=1; i<=baseline_sample_count; i++ )); do
  sample baseline api "$probe_url" "$i" "$primary_log" 3
  if [[ -n "$app_url" ]]; then
    sample baseline app "$app_url" "$i" "$app_log" 8
  fi
  sleep "$probe_interval_seconds"
done
if [[ -n "$status_url" ]]; then
  sample baseline status "$status_url" 1 "$status_log" 3
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
sample rollout api "$probe_url" "$((sample_number + 1))" "$primary_log" 3
if [[ -n "$app_url" ]]; then
  sample rollout app "$app_url" "$((sample_number + 1))" "$app_log" 8
fi
if [[ -n "$status_url" ]]; then
  sample rollout status "$status_url" "$((sample_number + 1))" "$status_log" 3
fi
for pid in "${secondary_pids[@]:-}"; do
  [[ -n "$pid" ]] || continue
  wait "$pid" || true
done
trap - INT TERM

gate_status=0
if summary="$(python3 - "${logs[@]}" <<'PY'
import collections
import math
import sys

samples = collections.defaultdict(list)
for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as stream:
        for line in stream:
            phase, target, sequence, observed_at, status, elapsed, connect, first_byte, curl_exit, error = line.rstrip("\n").split("\t", 9)
            samples[target].append((phase, int(sequence), observed_at, int(status), float(elapsed), float(connect), float(first_byte), int(curl_exit), error))

print("### Control-plane activation public-path observations")
print()
print("API readiness and canary app are independent gates after healthy baselines. Public status is diagnostic only.")
print()
print("| Path | Baseline | Rollout samples | Successful | Failed | Max latency |")
print("| --- | ---: | ---: | ---: | ---: | ---: |")
gate_failed = False
rows = []
details = []
for target in ("api", "app", "status"):
    if target not in samples:
        continue
    baseline = [sample for sample in samples[target] if sample[0] == "baseline"]
    rollout = [sample for sample in samples[target] if sample[0] == "rollout"]
    good = lambda sample: sample[7] == 0 and 200 <= sample[3] < 300
    baseline_successful = [sample for sample in baseline if good(sample)]
    successful = [sample for sample in rollout if good(sample)]
    failed = [sample for sample in rollout if not good(sample)]
    baseline_ready = bool(baseline) and len(baseline_successful) == len(baseline)
    if target != "status" and baseline_ready and failed:
        gate_failed = True
    maximum_latency_ms = math.ceil(1000 * max((sample[4] for sample in rollout), default=0))
    rows.append(f"| {target} | {len(baseline_successful)}/{len(baseline)} | {len(rollout)} | {len(successful)} | {len(failed)} | {maximum_latency_ms} ms |")
    if not baseline_ready:
        details.append(f"{target}: rollout attribution is **inconclusive** because its baseline was not healthy.")
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
