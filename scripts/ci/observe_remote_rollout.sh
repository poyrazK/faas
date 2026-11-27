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
# The canary app gate needs an app that is always deployed. rc.305's baseline
# got 404 from the deleted gregale-api-demo, and rc.366-369's from e2e-probe,
# which did not survive the OVH cutover and the move to gregale.app, so the
# gate gave no signal. The platform serving probe is the app the post-deploy
# monitoring gate already requires to serve. The ROLLOUT_APP_PROBE_URL
# repository or environment variable retargets it.
app_probe_url="${ROLLOUT_APP_PROBE_URL:-https://serving-probe.gregale.app/}"
if ! [[ "$app_probe_url" =~ ^https://[A-Za-z0-9.-]+(/[A-Za-z0-9._/-]*)?$ ]]; then
  echo "ROLLOUT_APP_PROBE_URL must be an https URL without a query, got '$app_probe_url'" >&2
  exit 2
fi
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
# A control plane rebuilt from backups has no /opt/faas/current and no API
# until this activation (docs/runbooks/ControlPlaneStateBackup.md). Only then
# is the activation judged on the API becoming ready instead of on a healthy
# baseline. An unreadable answer stops here rather than relaxing the gate.
release_state="$(ssh "${ssh_args[@]}" "$target" \
  'if test -e /opt/faas/current || test -L /opt/faas/current; then echo active; else echo none; fi')"
case "$release_state" in
  active) baseline_mode=('ROLLOUT_REQUIRE_API_BASELINE=true') ;;
  none)
    baseline_mode=('ROLLOUT_FIRST_ACTIVATION=true')
    echo "::notice::the control plane has no active release: first activation, gated on the API becoming ready" >&2
    ;;
  *) echo "could not read the control plane's active release: '$release_state'" >&2; exit 2 ;;
esac
printf -v remote_command '%q ' env \
  "RUNNER_TEMP=$remote_dir" "GITHUB_RUN_ID=$run_id" "GITHUB_RUN_ATTEMPT=$attempt" \
  "GITHUB_STEP_SUMMARY=$remote_dir/summary.txt" \
  "ROLLOUT_ACTIVATION_MARKER=$remote_dir/activated" \
  "ROLLOUT_BASELINE_SAMPLE_COUNT=$baseline" \
  "${baseline_mode[@]}" \
  "ROLLOUT_APP_PROBE_URL=$app_probe_url" \
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
