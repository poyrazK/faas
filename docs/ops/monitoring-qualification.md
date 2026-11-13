# Qualify production monitoring delivery

Use the manual `monitoring-qualification` GitHub Actions workflow to exercise
the deployed Alertmanager page/warn routes and retain test-bound operator
receipt attestations. The default **prepare** operation reads receiver intent.
**Exercise sends real page and warn notifications**, including configured
emergency-priority phone notifications. The workflow is implemented but has
not been run against production as part of this change.

## Prerequisites

The selected `production` or `production-us` GitHub environment needs existing
`CP_HOST`/`CP_SSH_KEY` (or the existing DO fallbacks), `COMPUTE_KNOWN_HOSTS` and
`MONITORING_OPERATOR_TOKEN`. The operator bearer must satisfy existing admin
scope, MFA and allowlist policy for the loopback notification-health API.
The control plane needs Python 3, current receiver-health support, and converged
receiver inventory/collector roles. Token values and provider secret files
never enter artifacts or command arguments.

Review [notification delivery](notification-delivery.md),
[post-deployment monitoring](post-deployment-monitoring.md), and
[scheduled-canary heartbeat activation](synthetic-canary-heartbeat.md).
Qualification does not provision secrets, arm external monitors, deploy code
or replace the first scheduled-canary and post-deployment activation checks.
Notify the on-call operator through your established procedure before choosing
exercise mode, and arrange access to every configured destination.

## 1. Prepare

Dispatch `monitoring-qualification` with `mode=prepare` and the target
`deploy_environment`. Download the
`monitoring-qualification-<fleet>-<run_id>-<attempt>` artifact.

`report.json` identifies the fleet, active-release pointer, expected
receiver/integration groups and their inventory digest. Page and heartbeat
roles must be present; warn is included when expected. Every receipt check
starts unknown, including quiet receivers with no prior success observation.
Missing, stale, duplicate, unexpected or inconsistent intent stops preparation.
The procedure reads deployment intent, not receiver addresses or secret files.

Prepare and exercise exit **3 / unknown** while receipts are unrecorded; their
workflow runs are red and still upload evidence. That status cannot qualify
monitoring as successful. Review the report reason to distinguish expected
pending receipts from an unavailable inventory or failed operation.

## 2. Exercise production page and warn routes

Dispatch the workflow again with `mode=exercise` for the same fleet. Each run
attempt has a new test ID such as `mq-123456789-1`. Alert names and summaries
contain that ID. Check the new exercise artifact, not a prepare artifact.

The helper rechecks current release and receiver intent before sending one
page and, when expected, one warn alert through the existing Alertmanager
route tree. Distinct test families prevent page-over-warn inhibition and avoid
joining ordinary warn digests. It waits 90 seconds and explicitly resolves the
same label sets. Alerts have a fixed 15-minute lifetime even if the runner dies.
Cleanup is attempted after an uncertain POST, HTTP/SSH failure or handled
cancellation. A hard kill can prevent cleanup; inspect the report and let the
bounded alerts expire. Do not change the expiry to keep a test running.

The page route normally sends firing after its 10-second group wait and
recovery on its five-minute group interval. Warn firing uses the initial
30-second wait; its recovery can wait for the **one-hour group interval**.
Custom deployed timing can differ. Accepted alert submissions or increasing
transport counters do not establish either receipt. Record each actual
firing and recovery observation with its matching test ID. Silences and normal
inhibition remain in effect; missing delivery stays unverified or failed.

This directly exercises Alertmanager and its production adapters. It does not
test Prometheus rule evaluation. Use the existing isolated local drill for
that routing/evaluation fixture and existing continuous monitors for liveness.

## 3. Qualify the independent deadline path separately

Keep the live `FaasWatchdog` and scheduled-canary heartbeat checks running.
Never stop Prometheus/Alertmanager, silence the watchdog, or send extra pings
to those checks to manufacture qualification evidence.

Create a **separate dedicated qualification check** in the external service.
Give it the exercise test ID as its name and use the same independently
delivered notification destinations/settings as the production monitor. A
short deadline, such as 60 seconds, keeps this procedure bounded; it does not
exercise the production monitor's configured period/grace. Use the service's
documented client or UI for this separate check:

1. Send its initial success ping and confirm the check is armed and received it.
2. Let that qualification deadline expire without sending further pings. Observe
   its actual missing-ping alarm at the independent on-call destination.
3. Resume the qualification check's success ping and observe its recovery.

Record the initial ping, alarm and recovery times and receipt fingerprints.
The alarm must occur at least `deadline_seconds` after the recorded ping, and
recovery must follow the alarm. A provider's test-message button verifies only
its notification path and cannot substitute for this deadline test. Retire
the dedicated check after retaining its alarm and recovery evidence.

The report records this as an operator-attested separate-check test. It always
discloses that live-watchdog interruption and the production deadline were not
exercised. Check the live production monitor's armed state and normal receipts
separately as described in the [watchdog runbook](../runbooks/FaasWatchdog.md).

## 4. Record receipts

The exercise artifact includes `receipt-template.json`. Complete it locally;
do not upload raw chat messages, email addresses, webhook URLs or credentials.
Remove unobserved rows for a partial report. Use `not_received` only when you
have deliberately judged a delivery missing after allowing its deployed timing.

Keep `qualification_id`, `fleet`, `release` and `inventory_sha256` unchanged.
Set `receiver_configuration_unchanged=true` only after confirming destinations
and delivery settings have not changed since exercise. The inventory digest
covers receiver/integration names; it cannot detect changes to addresses,
secret files or multiple destinations within one group.

Fill `external_check.check_id_sha256` with a fingerprint of the dedicated check's
non-secret identity, its actual qualification `deadline_seconds` (1–3600), and
`same_notification_settings=true` after checking its delivery settings.

Each observation contains exactly:

| Field | Value |
|---|---|
| `receiver`, `integration` | The expected group from the template. |
| `event` | `firing`/`resolved`, or `watchdog_ping`/`watchdog_alarm`/`watchdog_recovery`. |
| `observed_at` | Actual receipt time as a timezone-qualified ISO timestamp. |
| `result` | `received` or `not_received`. |
| `receipt_sha256` | A unique lowercase SHA-256 fingerprint for a received event; null for a missing receipt. |
| `all_destinations_observed` | True only when every configured destination aggregated by this group was observed; false for a missing receipt. |

Fingerprint the actual event-specific receipt identity or a privately retained
evidence file. Include the test ID/event/destination identities when a provider
reuses one incident ID for firing and recovery. For a group with multiple
destinations, fingerprint their combined evidence and confirm all destinations.
A fingerprint is a reference for later review; it is not automatic proof that
the receipt exists. Keep original evidence in the established private on-call
evidence location. A local hidden prompt can hash an identity without putting
it in shell history:

```bash
python3 -c 'import getpass, hashlib; print(hashlib.sha256(getpass.getpass("Event-specific receipt identity: ").encode()).hexdigest())'
```

Within **24 hours of the exercise**, dispatch `mode=record` with the completed
exercise's `source_run_id`, `source_run_attempt` and the completed template as
`receipts_json`. Select the same deployment environment. Recording downloads
the exact run/attempt artifact, validates workflow identity/event/commit through
GitHub and checks the current release and expected inventory again. A prepare
artifact, unconfirmed submission/cleanup, changed fleet/release/inventory,
expired exercise, duplicate receipt, mismatched test ID or invalid timestamp
cannot qualify a route. The original validity deadline is retained.

## Report and exit states

| Exit | State | Meaning |
|---|---|---|
| 0 | `operator_attested` | All expected events have matching positive operator attestations. |
| 1 | `failed` | An expected event was explicitly reported missing. |
| 3 | `unknown` | Preparation, pending/partial receipts, unavailable evidence or invalid qualification input. |
| 2 | local error | Invalid local configuration or report I/O failure. |

Each group and event retains its own state. A reported failure takes precedence
over unknown peers. Missing observations never become zero failures or passing
receipt checks. Reports retain the source artifact SHA-256, workflow run and
attempt, exercise/recorder commits, actor, receipt times and recording run.
They make the manual provenance explicit and do not query provider receipt APIs.

Reports are written atomically with owner-only local permissions. GitHub attempts
artifact upload on every job outcome and retains each immutable run artifact for
30 days. Artifact access follows repository permissions. Cancellation or failed
checkout/runner/upload can prevent an artifact; its absence is never success.
Use the original exercise artifact for later complete/partial recording runs;
observations are not silently merged from previous attestations.

An artifact is historical evidence. Always inspect `valid_until`; it expires
24 hours after exercise, and receiver/delivery changes invalidate it sooner.
No continuous health endpoint consumes this report as a delivery guarantee.
Run `make monitoring-qualification-test` for offline tests; these use local
HTTP fixtures and stubbed SSH and send no production notifications.
