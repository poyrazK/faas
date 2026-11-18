#!/usr/bin/env bash
# Submit a failed canary notification, with a bounded independent fallback.
# The workflow has already failed; a successful submission does not make it green
# and does not establish provider acceptance or operator receipt.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ssh_dir="${RUNNER_TEMP:?RUNNER_TEMP is required}/canary-ssh"
reason="$(cat "$ssh_dir/status" 2>/dev/null || true)"
[[ "$reason" =~ ^ssh_[a-z_]+$ ]] || reason=ssh_setup_incomplete
description="$(tail -c 2000 "$RUNNER_TEMP/canary.log" 2>/dev/null || echo 'no output captured')"
summary="the deployment canary could not establish customer deploy health on release ${ACTIVE_RELEASE:-unknown}"
if [[ "${CANARY_HEARTBEAT_OUTCOME:-}" == failure ]]; then
  summary="the deployment canary passed on release ${ACTIVE_RELEASE:-unknown}, but its independent heartbeat could not be reported"
  description='Check CANARY_HEARTBEAT_URL and the fixed error code in the heartbeat step. No provider response or secret URL is logged.'
elif [[ "${CANARY_PROBE_OUTCOME:-}" == failure ]]; then
  summary="a customer could not deploy and invoke on release ${ACTIVE_RELEASE:-unknown}"
elif [[ ! -f "$ssh_dir/ready" ]]; then
  description="Control-plane SSH preflight failed: ${reason}. Customer deployment health was not tested."
fi
run_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"

if [[ -f "$ssh_dir/ready" ]]; then
  # Bound the complete SSH/remote-curl attempt, leaving time for fallback.
  # printf %q also protects the REMOTE shell's second parse of the command.
  if timeout --signal=TERM --kill-after=5s 120s bash "$here/canary_ssh.sh" run \
    "env CANARY_RUN_URL=$(printf %q "$run_url") bash -s -- $(printf %q "$summary") $(printf %q "$description")" \
    < "$here/canary_alert.sh" >/dev/null 2>&1; then
    echo '::error::canary-notify: notification_submitted_alertmanager'
    exit 0
  fi
  echo 'canary-notify: alertmanager_submission_failed'
else
  # Never retry the same unavailable or untrusted SSH path before fallback.
  echo 'canary-notify: alertmanager_skipped_ssh_not_ready'
fi

if [[ -z "${CANARY_FALLBACK_WEBHOOK_URL:-}" ]]; then
  echo '::error::canary-notify: fallback_unconfigured' >&2
  exit 1
fi
# Never log curl's raw errors or response: they can contain the secret URL.
# Redirects are not accepted as a successful notification submission.
if http_status="$(jq -n --arg text "Gregale synthetic canary failed; Alertmanager submission was unavailable: ${summary}. ${run_url}" '{text: $text}' |
  curl --disable --fail --silent --connect-timeout 3 --max-time 15 --proto '=https' \
    --header 'Content-Type: application/json' --data @- \
    --output /dev/null --write-out '%{http_code}' --url "$CANARY_FALLBACK_WEBHOOK_URL" 2>/dev/null)" \
    && [[ "$http_status" =~ ^2[0-9][0-9]$ ]]; then
  echo '::error::canary-notify: notification_submitted_fallback'
  exit 0
fi
echo '::error::canary-notify: fallback_submission_failed' >&2
exit 1
