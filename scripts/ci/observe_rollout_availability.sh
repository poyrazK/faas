#!/usr/bin/env bash
# Observe the unauthenticated customer path while a rollout command runs.
# A fully healthy baseline turns customer-path failures into a rollout gate.
# An unhealthy baseline is reported as inconclusive instead of being falsely
# attributed to the wrapped command. The wrapped command's own failure always
# takes precedence.
set -euo pipefail

if (( $# < 3 )) || [[ "$2" != "--" ]]; then
  echo "usage: $0 URL -- COMMAND [ARG ...]" >&2
  exit 2
fi

probe_url="$1"
shift 2
probe_interval_seconds="${ROLLOUT_PROBE_INTERVAL_SECONDS:-0.25}"
baseline_sample_count="${ROLLOUT_BASELINE_SAMPLE_COUNT:-3}"
probe_proxy="${ROLLOUT_PROBE_PROXY:-}"
probe_log="${RUNNER_TEMP:-/tmp}/gregale-rollout-availability-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}.tsv"
: > "$probe_log"

curl_proxy_args=()
if [[ -n "$probe_proxy" ]]; then
  curl_proxy_args=(--proxy "$probe_proxy")
fi

sample_number=0
sample() {
  phase="$1"
  sample_number=$((sample_number + 1))
  separator="?"
  [[ "$probe_url" == *\?* ]] && separator="&"
  request_url="${probe_url}${separator}rollout_probe=${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${sample_number}"
  observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  result="$(curl "${curl_proxy_args[@]}" --silent --show-error --location \
    --output /dev/null --write-out $'%{http_code}\t%{time_total}' \
    --connect-timeout 1 --max-time 3 \
    --header 'Cache-Control: no-cache' \
    "$request_url" 2>/dev/null)" || true
  result_pattern=$'^[0-9]{3}\t[0-9]+([.][0-9]+)?$'
  if [[ ! "$result" =~ $result_pattern ]]; then
    result=$'000\t3.000000'
  fi
  printf '%s\t%d\t%s\t%s\n' "$phase" "$sample_number" "$observed_at" "$result" >> "$probe_log"
}

# Establish that the observer can reach the customer path before the wrapped
# command mutates production. Without this baseline, edge policy or runner
# egress failures are not attributable to the rollout.
for _ in $(seq 1 "$baseline_sample_count"); do
  sample baseline
  sleep "$probe_interval_seconds"
done

"$@" &
command_pid=$!

trap '
  kill -TERM "$command_pid" 2>/dev/null || true
  wait "$command_pid" 2>/dev/null || true
' INT TERM

while kill -0 "$command_pid" 2>/dev/null; do
  sample rollout
  sleep "$probe_interval_seconds"
done

command_status=0
wait "$command_pid" || command_status=$?
sample rollout
trap - INT TERM

summary="$(python3 - "$probe_log" <<'PY'
import collections
import math
import sys

samples = []
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        phase, sequence, observed_at, status, elapsed = line.rstrip("\n").split("\t")
        samples.append((phase, int(sequence), observed_at, int(status), float(elapsed)))

baseline = [sample for sample in samples if sample[0] == "baseline"]
rollout = [sample for sample in samples if sample[0] == "rollout"]
baseline_successful = [sample for sample in baseline if 200 <= sample[3] < 300]
successful = [sample for sample in rollout if 200 <= sample[3] < 300]
failed = [sample for sample in rollout if not 200 <= sample[3] < 300]
longest_failure_run = current_failure_run = 0
for sample in rollout:
    if 200 <= sample[3] < 300:
        current_failure_run = 0
    else:
        current_failure_run += 1
        longest_failure_run = max(longest_failure_run, current_failure_run)

availability = 100 * len(successful) / len(rollout) if rollout else 0
maximum_latency_ms = math.ceil(1000 * max((sample[4] for sample in rollout), default=0))
baseline_ready = bool(baseline) and len(baseline_successful) == len(baseline)
status_counts = ", ".join(
    f"{status}: {count}" for status, count in sorted(collections.Counter(sample[3] for sample in rollout).items())
)
print("### Control-plane activation customer-path observations")
print()
print(f"Baseline: **{'ready' if baseline_ready else 'unavailable'}** ({len(baseline_successful)}/{len(baseline)} successful samples).")
if not baseline_ready:
    print("Rollout attribution is **inconclusive** because the observer could not reach the customer path before mutation began.")
print()
print("| Samples | Successful | Failed | Observed availability | Longest failed run | Max latency |")
print("| ---: | ---: | ---: | ---: | ---: | ---: |")
print(f"| {len(rollout)} | {len(successful)} | {len(failed)} | {availability:.3f}% | {longest_failure_run} samples | {maximum_latency_ms} ms |")
print()
print(f"HTTP status counts: {status_counts or 'none'}.")
if failed:
    print()
    print(f"Failed samples ran from {failed[0][2]} through {failed[-1][2]} UTC.")
PY
)"

printf '%s\n' "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf '%s\n' "$summary" >> "$GITHUB_STEP_SUMMARY"
fi

if (( command_status != 0 )); then
  exit "$command_status"
fi

if awk -F '\t' '
  $1 == "baseline" { baseline_total++; if ($4 >= 200 && $4 < 300) baseline_success++ }
  $1 == "rollout" && !($4 >= 200 && $4 < 300) { rollout_failed++ }
  END { exit !(baseline_total > 0 && baseline_success == baseline_total && rollout_failed > 0) }
' "$probe_log"; then
  echo "customer path lost after a healthy pre-rollout baseline" >&2
  exit 1
fi

exit 0
