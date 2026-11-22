#!/usr/bin/env bash
# Submit a failed canary notification or a controlled fallback delivery test.
# The workflow records submission separately from operator receipt.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fleet="${CANARY_FLEET:-unknown}"
repository_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}"
run_url="${repository_url}/actions/runs/${GITHUB_RUN_ID}"

send_fallback() {
  local text="$1" url="${CANARY_FALLBACK_WEBHOOK_URL:-}" http_status
  if [[ -z "$url" ]]; then
    echo '::error::canary-notify: fallback_unconfigured' >&2
    return 1
  fi
  if [[ "$url" != https://?* || "$url" =~ [[:space:]] || "$url" == *'"'* || "$url" == *\\* ]]; then
    echo '::error::canary-notify: fallback_url_invalid' >&2
    return 1
  fi
  # Keep the credential out of curl's arguments. Reject characters that
  # could escape the quoted config value, and suppress raw provider errors.
  if http_status="$(jq -n --arg text "$text" '{text: $text}' |
    curl --disable --fail --silent --connect-timeout 3 --max-time 15 --proto '=https' \
      --header 'Content-Type: application/json' --data @- \
      --output /dev/null --write-out '%{http_code}' \
      --config <(printf 'url = "%s"\n' "$url") 2>/dev/null)" \
      && [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
    return 0
  fi
  echo '::error::canary-notify: fallback_submission_failed' >&2
  return 1
}

if [[ "${1:-}" == --test-fallback ]]; then
  send_fallback "Gregale controlled canary fallback delivery test for ${fleet}. Run: ${run_url}"
  echo '::notice::canary-notify: test_notification_submitted_fallback'
  exit 0
fi

ssh_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/canary-ssh"
reason="$(cat "$ssh_dir/status" 2>/dev/null || true)"
[[ "$reason" =~ ^ssh_[a-z_]+$ ]] || reason=ssh_setup_incomplete
description="$(tail -c 2000 "$RUNNER_TEMP/canary.log" 2>/dev/null || echo 'no output captured')"
release="${ACTIVE_RELEASE:-unknown}"
last_successful_check=unknown
impact=unknown
summary="the deployment canary could not establish customer deploy health on release ${release}"
if [[ "${CANARY_HEARTBEAT_OUTCOME:-}" == failure ]]; then
  summary="the deployment canary passed on release ${release}, but its independent heartbeat could not be reported"
  description='Check CANARY_HEARTBEAT_URL and the fixed error code in the heartbeat step. No provider response or secret URL is logged.'
  stage='Independent heartbeat'
  first_action='Open the heartbeat step and check the external monitor for its success ping.'
  if [[ "${CANARY_PROBE_OUTCOME:-}" == success ]]; then
    last_successful_check='Deploy and invoke passed in this run'
    impact='The synthetic deployment and invocation succeeded. Wider customer impact is unknown.'
  fi
elif [[ "${CANARY_PROBE_OUTCOME:-}" == failure ]]; then
  summary="the production deploy-and-invoke probe failed on release ${release}"
  stage='Deploy and invoke'
  first_action='Open Deploy and invoke through production and find the first failing step in the probe log.'
  impact='The synthetic deployment check failed. Wider customer impact is unknown.'
elif [[ ! -f "$ssh_dir/ready" ]]; then
  description="Control-plane SSH preflight failed: ${reason}. Customer deployment health was not tested."
  stage="Control-plane SSH preflight (${reason})"
  first_action="Open the failed SSH preflight step and follow the runbook for ${reason}."
else
  stage='Live release and fleet discovery'
  first_action='Open Resolve the live release and fleet size and check the release pointer and active-node inventory.'
fi
runbook_url="${repository_url}/blob/main/docs/runbooks/FaasSyntheticCanaryFailed.md"
triage="$(jq -cn --arg fleet "$fleet" --arg release "$release" \
  --arg last_success "$last_successful_check" --arg impact "$impact" \
  --arg stage "$stage" --arg action "$first_action" \
  '{fleet: $fleet, active_release: $release, last_successful_check: $last_success,
    customer_impact: $impact, failure_stage: $stage, first_action: $action}')"

if [[ -f "$ssh_dir/ready" ]]; then
  # Bound the complete SSH/remote-curl attempt, leaving time for fallback.
  # printf %q also protects the REMOTE shell's second parse of the command.
  if timeout --signal=TERM --kill-after=5s 120s bash "$here/canary_ssh.sh" run \
    "env CANARY_RUN_URL=$(printf %q "$run_url") CANARY_TRIAGE_JSON=$(printf %q "$triage") bash -s -- $(printf %q "$summary") $(printf %q "$description")" \
    < "$here/canary_alert.sh" >/dev/null 2>&1; then
    echo '::error::canary-notify: notification_submitted_alertmanager'
    exit 0
  fi
  echo 'canary-notify: alertmanager_submission_failed'
else
  # Never retry the same unavailable or untrusted SSH path before fallback.
  echo 'canary-notify: alertmanager_skipped_ssh_not_ready'
fi

fallback_text="$(printf '%s\n' \
  "Gregale: ${summary}" \
  "First check: ${first_action}" "Fleet: ${fleet}" "Customer impact: ${impact}" \
  "Stage: ${stage}" "Last successful check: ${last_successful_check}" "Active release: ${release}" \
  "Failed run: ${run_url}" "Runbook: ${runbook_url}" "Delivery: Alertmanager submission unavailable")"
send_fallback "$fallback_text"
echo '::error::canary-notify: notification_submitted_fallback'
