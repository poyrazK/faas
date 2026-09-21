#!/usr/bin/env bash
# canary_alert.sh — push a synthetic-canary failure into Alertmanager.
#
# The platform already has ~170 Prometheus rules and a wired Alertmanager
# (page → operator email + Pushover). What it does not have is a way for an
# out-of-band prober to reach that same routing, so a failing canary would
# otherwise be a red GitHub badge nobody is paged for.
#
# Posting a firing alert rather than inventing a second notification path
# means the canary inherits the receivers, the silences, and the grouping that
# operators already use.
#
# No explicit resolve is sent. endsAt is set a little beyond the canary's own
# cadence, so a passing run simply lets the alert lapse while a second failure
# refreshes it. That is the standard Alertmanager pattern for an external
# prober and it removes the failure mode where a resolve is lost and the page
# repeats hourly forever.
#
# Usage:
#   canary_alert.sh <summary> <description>
#
# Environment:
#   ALERTMANAGER_URL   default http://127.0.0.1:9094
#   CANARY_ALERTNAME   default FaasSyntheticCanaryFailed
#   CANARY_TTL_MINUTES default 180 (cadence is 6h; see synthetic-canary.yml)
#   CANARY_RUN_URL     optional link back to the workflow run
set -euo pipefail

summary="${1:?usage: canary_alert.sh <summary> <description>}"
description="${2:?usage: canary_alert.sh <summary> <description>}"

alertmanager_url="${ALERTMANAGER_URL:-http://127.0.0.1:9094}"
alertname="${CANARY_ALERTNAME:-FaasSyntheticCanaryFailed}"
ttl_minutes="${CANARY_TTL_MINUTES:-180}"
run_url="${CANARY_RUN_URL:-}"

[[ "$ttl_minutes" =~ ^[0-9]+$ ]] || {
  echo "canary_alert: CANARY_TTL_MINUTES must be an integer" >&2
  exit 2
}

starts_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ends_at="$(date -u -d "+${ttl_minutes} minutes" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
  date -u -v "+${ttl_minutes}M" +%Y-%m-%dT%H:%M:%SZ)"

# jq builds the body so a summary containing a quote or a newline cannot break
# out of the JSON. The canary's description carries script output.
payload="$(jq -n \
  --arg alertname "$alertname" \
  --arg summary "$summary" \
  --arg description "$description" \
  --arg runURL "$run_url" \
  --arg startsAt "$starts_at" \
  --arg endsAt "$ends_at" \
  '[{
      labels: {
        alertname: $alertname,
        severity: "page",
        component: "platform",
        family: "synthetic_canary"
      },
      annotations: ({
        summary: $summary,
        description: $description,
        runbook_url: "https://github.com/poyrazK/faas/blob/main/docs/runbooks/FaasSyntheticCanaryFailed.md"
      } + (if $runURL == "" then {} else {run_url: $runURL} end)),
      startsAt: $startsAt,
      endsAt: $endsAt
   }]')"

curl --fail --silent --show-error \
  --retry 5 --retry-delay 2 --retry-all-errors \
  --connect-timeout 3 --max-time 15 \
  --header 'Content-Type: application/json' \
  --data "$payload" \
  "${alertmanager_url%/}/api/v2/alerts" >/dev/null

echo "canary_alert: posted ${alertname} (severity=page, expires ${ends_at})"
