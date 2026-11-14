# Watch the scheduled deployment canary

The production deploy canary runs at minute 37 every six hours (UTC), with a
45-minute job limit. Its failure page and chat fallback depend on a workflow
actually running. A dedicated external missing-run monitor covers disabled
schedules, jobs stuck waiting for a runner or environment approval, cancelled
or timed-out runs, and lost runner processes.

## Set up before rollout

1. Create a **dedicated** check in an independent heartbeat service, such as
   Healthchecks.io or an equivalent service. Use a success-ping endpoint that
   accepts an empty HTTPS POST and returns a 2xx response. Configure an interval
   of six hours and a grace of two hours: absence of success should alarm after
   eight hours. This allows scheduling jitter and the 45-minute job budget.
   Reassess the deadline if either workflow cadence or runtime changes.
2. Route the external monitor's alerts to the on-call destination through that
   service's own notification delivery. It must work when Gregale, its
   Prometheus and its Alertmanager are unavailable. Give the check a name such
   as `Gregale scheduled deployment canary` and link it to the GitHub Actions
   `synthetic-canary` workflow.
3. Store the **success** URL as `CANARY_HEARTBEAT_URL` in the repository's
   GitHub **production environment secrets**. The canary job already uses that
   environment. The URL is a credential; do not put it in variables, commit it,
   or copy it into issues. Provision it before merging the workflow change.
4. Watch the first automatic scheduled run. The heartbeat step must print
   `canary-heartbeat: scheduled_success_reported`, and the external service
   must show that check's new success at the matching time. Confirm the check
   is armed: some services leave new checks paused until their first ping.
   Until this succeeds, monitoring activation is pending.
5. Qualify the independent notification path and missed-ping alarm using the
   service's test facility or a separate short-interval qualification check
   with the same delivery settings. Record the alarm and recovery receipts.
   A successful HTTP response alone does not prove an armed deadline or a
   notification reaching the operator.

Use a new check and URL for this workflow. Reusing `FaasWatchdog`, a node's
serving-probe check, or another job's success URL lets unrelated activity hide
a stopped deployment canary. No other job should ping this check.

## What refreshes the deadline

Only a **first-attempt scheduled run** whose job and deployment probe both
succeeded may report success. The workflow condition and Python helper both
enforce this. The heartbeat is sent after production acceptance completes,
directly from the GitHub runner to the external service.

Failed, skipped, cancelled or timed-out probes never ping. Manual
`workflow_dispatch` runs and reruns of old scheduled runs also do not ping;
they cannot make a stopped schedule look current. No start or failure ping
resets the success deadline. A later successful automatic run is required to
restore this check after recovery.

The client uses normal TLS verification, rejects redirects, ignores proxy
environment variables, and posts an empty body. It retries transient failures
at most three times with a five-second request timeout and one-second gaps.
The step has a one-minute outer limit. Response bodies, URL values and raw
exceptions are never logged.

For a qualifying scheduled run, a missing or invalid URL fails the heartbeat
step. An endpoint that cannot accept the ping also fails the step. The
existing Alertmanager page and direct chat fallback then identify that
acceptance passed but its independent heartbeat could not be reported. These
immediate pages complement the external missing-success deadline; they do not
replace it.

## Investigate an alarm

Open the workflow and compare the last successful **automatic, first-attempt**
run with the external check's last accepted success. Check whether the workflow
is disabled, GitHub delayed a scheduled run, a job awaits approval or a runner,
the job failed or was cancelled, or the heartbeat endpoint failed.

Heartbeat failures use fixed error codes:

| Code | Action |
|---|---|
| `heartbeat_url_unconfigured` | Provision `CANARY_HEARTBEAT_URL` in the production environment. |
| `invalid_heartbeat_url` | Correct the success URL: HTTPS, no whitespace, user/password credentials or fragment. Do not print it to diagnose. |
| `heartbeat_delivery_failed` | Check the provider's status, URL validity, TLS and runner egress. Redirects and HTTP errors are failures. |
| `skipped_ineligible_run` | Expected for manual, rerun or unsuccessful contexts. Such runs do not establish schedule liveness. |

Fix the underlying schedule, runner, deploy path or monitor configuration, then
verify the next automatic run and the external service's recovery. The
existing [synthetic-canary runbook](../runbooks/FaasSyntheticCanaryFailed.md)
covers deployment failures. Avoid ad-hoc pings that hide a missing schedule.

## Local checks

`make canary-heartbeat-test` exercises eligibility and failures using Python's
standard library and disposable loopback TLS servers. Tests need Python 3 and
OpenSSL; they send no real notifications. CI runs them alongside the existing
canary alert-payload tests and validates the workflow with actionlint.

Repository tests qualify client and workflow behavior. External check setup,
secret provisioning, first scheduled receipt and independent alarm delivery
remain operational activation steps.
