#!/usr/bin/env bash
# Observe the unauthenticated customer path while a rollout command runs.
# Probe failures are evidence in the job summary, not a reason to mask the
# wrapped command's result. The post-rollout platform gate remains authoritative.
set -euo pipefail

if (( $# < 3 )) || [[ "$2" != "--" ]]; then
  echo "usage: $0 URL -- COMMAND [ARG ...]" >&2
  exit 2
fi

probe_url="$1"
shift 2
probe_interval_seconds="${ROLLOUT_PROBE_INTERVAL_SECONDS:-0.25}"
probe_log="${RUNNER_TEMP:-/tmp}/gregale-rollout-availability-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}.tsv"
: > "$probe_log"

"$@" &
command_pid=$!

trap '
  kill -TERM "$command_pid" 2>/dev/null || true
  wait "$command_pid" 2>/dev/null || true
' INT TERM

sample_number=0
sample() {
  sample_number=$((sample_number + 1))
  separator="?"
  [[ "$probe_url" == *\?* ]] && separator="&"
  request_url="${probe_url}${separator}rollout_probe=${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-${sample_number}"
  observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  result="$(curl --silent --show-error --location \
    --output /dev/null --write-out $'%{http_code}\t%{time_total}' \
    --connect-timeout 1 --max-time 3 \
    --header 'Cache-Control: no-cache' \
    --user-agent "gregale-rollout-observer/${GITHUB_RUN_ID:-local}" \
    "$request_url" 2>/dev/null)" || true
  result_pattern=$'^[0-9]{3}\t[0-9]+([.][0-9]+)?$'
  if [[ ! "$result" =~ $result_pattern ]]; then
    result=$'000\t3.000000'
  fi
  printf '%d\t%s\t%s\n' "$sample_number" "$observed_at" "$result" >> "$probe_log"
}

while kill -0 "$command_pid" 2>/dev/null; do
  sample
  sleep "$probe_interval_seconds"
done

command_status=0
wait "$command_pid" || command_status=$?
sample
trap - INT TERM

summary="$(python3 - "$probe_log" <<'PY'
import math
import sys

samples = []
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        sequence, observed_at, status, elapsed = line.rstrip("\n").split("\t")
        samples.append((int(sequence), observed_at, int(status), float(elapsed)))

successful = [sample for sample in samples if 200 <= sample[2] < 300]
failed = [sample for sample in samples if not 200 <= sample[2] < 300]
longest_failure_run = current_failure_run = 0
for sample in samples:
    if 200 <= sample[2] < 300:
        current_failure_run = 0
    else:
        current_failure_run += 1
        longest_failure_run = max(longest_failure_run, current_failure_run)

availability = 100 * len(successful) / len(samples) if samples else 0
maximum_latency_ms = math.ceil(1000 * max((sample[3] for sample in samples), default=0))
print("### Control-plane activation customer-path observations")
print()
print("| Samples | Successful | Failed | Observed availability | Longest failed run | Max latency |")
print("| ---: | ---: | ---: | ---: | ---: | ---: |")
print(f"| {len(samples)} | {len(successful)} | {len(failed)} | {availability:.3f}% | {longest_failure_run} samples | {maximum_latency_ms} ms |")
if failed:
    print()
    print(f"Failed samples ran from {failed[0][1]} through {failed[-1][1]} UTC.")
PY
)"

printf '%s\n' "$summary"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf '%s\n' "$summary" >> "$GITHUB_STEP_SUMMARY"
fi

exit "$command_status"
