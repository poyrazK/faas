#!/usr/bin/env bash
# Observe from the control plane's trusted public egress without SSH forwarding.
set -euo pipefail
if (( $# != 3 )); then
  echo "usage: $0 SSH_TARGET SSH_KEY ACTIVATION_SCRIPT" >&2
  exit 2
fi
target="$1" key="$2" activation_script="$3"
run_id="${GITHUB_RUN_ID:-local}" attempt="${GITHUB_RUN_ATTEMPT:-1}"
baseline="${ROLLOUT_BASELINE_SAMPLE_COUNT:-3}"
[[ "$run_id" =~ ^[A-Za-z0-9_-]+$ && "$attempt" =~ ^[0-9]+$ && "$baseline" =~ ^[0-9]+$ ]] || exit 2
ssh_args=(-i "$key" -o StrictHostKeyChecking=yes)
remote_dir="$(ssh "${ssh_args[@]}" "$target" 'mktemp -d /tmp/gregale-rollout.XXXXXXXX')"
[[ "$remote_dir" =~ ^/tmp/gregale-rollout\.[A-Za-z0-9]+$ ]] || exit 2
local_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/gregale-rollout-returned.XXXXXXXX")"
# shellcheck disable=SC2329 # Invoked by the EXIT trap.
cleanup() {
  # shellcheck disable=SC2029 # mktemp path was validated above before quoting.
  ssh "${ssh_args[@]}" "$target" "rm -rf -- '$remote_dir'" || true
  rm -rf -- "$local_dir"
}
trap cleanup EXIT
scp "${ssh_args[@]}" scripts/ci/observe_rollout_availability.sh "$activation_script" "$target:$remote_dir/"
printf -v remote_command '%q ' env \
  "RUNNER_TEMP=$remote_dir" "GITHUB_RUN_ID=$run_id" "GITHUB_RUN_ATTEMPT=$attempt" \
  "GITHUB_STEP_SUMMARY=$remote_dir/summary.txt" \
  "ROLLOUT_ACTIVATION_MARKER=$remote_dir/activated" \
  "ROLLOUT_BASELINE_SAMPLE_COUNT=$baseline" \
  'ROLLOUT_REQUIRE_API_BASELINE=true' \
  'ROLLOUT_APP_PROBE_URL=https://gregale-api-demo.gregale.dev/' \
  'ROLLOUT_STATUS_PROBE_URL=https://api.gregale.dev/v1/status' \
  bash "$remote_dir/observe_rollout_availability.sh" 'https://api.gregale.dev/readyz' -- \
  bash "$remote_dir/$(basename "$activation_script")"
status=0
# shellcheck disable=SC2029 # Each remote argv value is encoded with printf %q.
ssh "${ssh_args[@]}" "$target" "$remote_command" || status=$?
collected=true
scp -r "${ssh_args[@]}" "$target:$remote_dir" "$local_dir/" || collected=false
returned="$local_dir/$(basename "$remote_dir")"
activated=false
if [[ "$collected" == true ]]; then
  [[ ! -f "$returned/activated" ]] || activated=true
  for evidence in "$returned"/gregale-rollout-availability-*.tsv; do
    [[ ! -f "$evidence" ]] || cp "$evidence" "${RUNNER_TEMP:-/tmp}/"
  done
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" && -f "$returned/summary.txt" ]]; then
    cat "$returned/summary.txt" >> "$GITHUB_STEP_SUMMARY"
  fi
elif (( status == 0 )); then
  echo "rollout evidence could not be retrieved" >&2
  status=1
fi
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
  printf 'activated=%s\n' "$activated" >> "$GITHUB_OUTPUT"
fi
exit "$status"
