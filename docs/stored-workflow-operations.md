# Run and monitor stored workflows

Find and cancel runs, watch a stored workflow's health, react to finished
runs, and get alerted when runs fail or stop being admitted. Recovering runs
that failed is covered in [Recover stored workflow runs](stored-workflow-recovery.md);
building workflows in [Build stored workflows](stored-workflow-authoring.md);
testing them in [Test stored workflows](stored-workflow-testing.md).

## Find workflow runs

The CLI can narrow an app's run history by exact workflow name, status, and
inclusive creation-time bounds:

```sh
gregale workflow list --app billing --workflow-name paid-invoice --status failed
gregale workflow list --app billing --created-after 2026-10-01T00:00:00Z \
  --created-before 2026-10-05T23:59:59Z --limit 50
```

Timestamps must use RFC3339, and `--created-after` must not be later than
`--created-before`. The CLI keeps the existing 50-run page size and supports
`--limit` up to 100 plus `--offset` for paging. The same filters are available
through the API:

```http
GET /v1/apps/billing/workflows/runs?workflow_name=paid-invoice&status=failed&created_after=2026-10-01T00:00:00Z&created_before=2026-10-05T23:59:59Z&limit=50
```

The filtered `total` and page use the same criteria. Results remain ordered newest
first; `limit` and `offset` keep their existing behavior. Go, Node, and Python
clients expose these filters. No dashboard editor is included.

## Cancel queued workflow runs

Pending runs can include a retry or a previously parked run, so use
`started_at` to identify work that has never begun. Preview only the run IDs you
intend to cancel, then repeat that exact selection with the explicit action:

```sh
gregale workflow list --app billing --workflow-name paid-invoice --status pending
gregale workflow cancel-queued-preview --app billing --workflow-name paid-invoice \
  --run-id <run-id>
gregale workflow cancel-queued --app billing --workflow-name paid-invoice \
  --run-id <run-id> --yes
```

You can select up to 20 runs per request by repeating `--run-id`. The optional
`--workflow-name` guards the selection against a name mismatch. Only pending
runs with no `started_at` value are eligible. The preview is advisory; the
action rechecks each run while holding the dispatcher claim lock. If dispatch
claims a run after preview, that run is reported as `already_started` or
`not_queued` and is left alone. A previously cancelled run is reported as
`already_cancelled`. Started runs, including retries and parked
waits, continue to use the single-run `workflows cancel` command.

The action commits eligible cancellations as one bounded batch. Outcomes are
returned for every selected ID; an eligible run becomes `failed` and receives
`cancelled_at`. Missing IDs and runs outside the selected app share the
`not_found` outcome.

The API exposes the same flow through
`POST /v1/apps/{slug}/workflows/runs:cancel-preview` and
`POST /v1/apps/{slug}/workflows/runs:cancel-queued` with a JSON body containing
`run_ids` and optional `workflow_name`.

## Inspect stored workflow health

Get a bounded operational summary for one saved stored workflow from the CLI, or use
the API directly:

```sh
gregale workflow stored health --app billing --name paid-invoice
gregale workflow stored health --app billing --name paid-invoice \
  --created-after 2026-10-01T00:00:00Z --created-before 2026-10-05T23:59:59Z
```

The CLI accepts RFC3339 bounds and prints run totals, current active and queued
counts, success rate, p50/p95 durations, latest run/success/failure IDs, and the
most frequently failed steps.
Use `--json` for the bounded machine-readable response. The summary omits run
inputs, outputs, and errors.

The API endpoint is:

```http
GET /v1/apps/billing/workflows/stored/paid-invoice/health?created_after=2026-10-01T00:00:00Z
```

The default window is the previous seven days, and callers can select at most
30 days with inclusive RFC3339 `created_after` and `created_before` timestamps.
The response includes run counts by status, current `active_run_count` and
`queued_run_count` (independent of the requested time window), success rate, p50/p95 durations,
the latest run/success/failure, and up to ten frequently failed logical steps.
Failures from individual loop items are grouped under their parent step. Run
inputs, outputs, and error messages are never included. Go, Node, and Python
clients expose this endpoint.

The optional `queue` object reports current waiting reasons, independently of
the historical window. The CLI prints its observation time, app dispatch
occupancy and limits, waiting/due/stale counts, oldest due age, and nonzero
reason counts. App occupancy includes all stored workflows and tenants in the app;
waiting counts belong to the selected stored workflow. Live running claims consume
dispatch capacity; parked waits and pending retries can still consume a
workflow's `max_concurrent_runs` budget.

| Queue reason | Meaning at observation |
| --- | --- |
| `ready` | Due and passes the app, tenant and workflow run limits |
| `scheduled` | Its next scheduling deadline is in the future |
| `retry_backoff` | Waiting for a pending step's retry deadline |
| `parked_wait` | An intentional wait has a future wake or timeout |
| `app_capacity` | The app's live dispatch claims fill its budget |
| `tenant_capacity` | That tenant's live claims within the app fill its budget |
| `workflow_capacity` | The run's captured workflow concurrency limit is full |

Each waiting run contributes to one reason. Future deadlines take precedence,
followed by app, tenant and workflow limits. Due timers, event/callback timeouts
and stale leases are included in admission checks; expired leases do not occupy
capacity. Oldest due age includes blocked work and starts at eligibility rather
than an intentional wait's start. The reason counts sum to `waiting_run_count`,
which includes started pending retries and parked waits as well as fresh runs.

`ready` describes dispatch admission. Fair turns, runtime gates, action budgets
and handler invocation limits can still delay execution. `parked_wait` groups
timers, conditions, events and callbacks; inspect the run's steps for details.
The snapshot does not estimate queue position, completion time or global worker
saturation. Older servers may omit `queue`; the CLI reports diagnostics as
unavailable. No customer payloads, errors or tenant identities are returned.

The list response reports when runtime,
plan, app maintenance, tenant requirements, or missing deployment prevents
execution. Saving and validating drafts alone does not enable the runtime.

## React to finished workflow runs

Subscribe an app webhook to `workflow.finished` to notify your service or an
external stored workflow tool when a durable run succeeds, fails, or reaches the dead
state:

```http
POST /v1/apps/billing/webhooks
Content-Type: application/json

{
  "target_url": "https://stored_workflow.example.com/gregale",
  "webhook_secret": "replace-with-a-random-secret",
  "event_filter": ["workflow.finished"],
  "delivery_format": "cloudevents"
}
```

The event is committed with the terminal status change and delivered through
the durable webhook outbox. A resumed run emits another event if it later
finishes; use the CloudEvent ID or webhook delivery ID to deduplicate retries.
The payload includes `app_id`, `run_id`, `workflow_name`, `status`,
`finished_at`, and `resume_count`. It omits run input, output, and error text.
Use `GET /v1/workflows/runs/{id}` with an authorized API key to fetch run
details. The webhook event filter is app-scoped; account-wide release
subscriptions do not receive workflow outcomes.

## Admission history and reliability alerts

Inspect the last 30 days of due schedule outcomes with
`gregale workflow schedule history --app reports --limit 100` or
`GET /v1/apps/{slug}/workflows/schedules/occurrences`. Both started and skipped
minutes are retained, including `skipped_quota` and `skipped_overlap`; each
started occurrence includes its run ID even after the run expires. History
contains no workflow input or output. Use `--platform-tenant-id` to inspect one
customer and `--cursor` with the returned next cursor for another page.
History starts when this version is deployed; earlier outcomes are not backfilled.
An expired history cursor returns an empty page.

To run a skipped occurrence after all, see
[Replay a skipped scheduled occurrence](stored-workflow-recovery.md#replay-a-skipped-scheduled-occurrence).

Tenant/workflow pairs share the app's active-run quota and are evaluated in
least-recently-admitted order. Admission priority persists through scheduler
restarts, skipped minutes, deployment changes, and run retention. Paused
schedules are excluded. This distributes scarce admissions across customers;
quota skips consume the selected occurrence and are not retried automatically.

### Automatically pause after repeated failures

Failure notifications can be paired with an opt-in admission pause per
published YAML or dashboard stored workflow. Configure a failure threshold, a
minimum completed sample count, and an observation window:

```sh
gregale workflow stored failure policy --app billing --name paid-invoice
gregale workflow stored failure policy --app billing --name paid-invoice \
  --enabled --expected-version 0 --failure-threshold 3 \
  --min-completed-runs 5 --window-seconds 300
```

The default policy is disabled. Defaults are three failures, five completed
samples and 300 seconds; thresholds and minimum samples accept 1..10000,
and windows accept 60..86400 seconds. Updates require the current policy
version. Enabling a new policy starts monitoring now; explicit failure resume
starts a new monitoring epoch. Policy edits preserve the existing epoch.
Failures count failed and dead runs completed within the window, while samples
also include successful runs. Cancellations, intermediate retries, and native
Customer Operations do not contribute.

The scheduler evaluates bounded pages of policies before scanning schedules.
A threshold breach latches a durable failure pause on an evaluator tick;
detection can take additional ticks when many policies are configured.
The pause blocks future app and tenant schedule admissions, new event routing,
inbound webhook admission, and admission of event recipients captured before
the pause. Captured recipients remain subject to normal routing retention and
retry budgets. Inbound webhook requests received during the pause have ignored
receipts with reason `stored_workflow_failure_paused`. Events published with no
eligible recipient are not replayed on resume. Existing pending, running and
waiting workflows continue; manual starts remain available for diagnosis.

The failure pause is separate from configured enabled intent. Publishing a
fix, ordinary `workflow stored resume`, disabling monitoring, or deleting and
recreating a definition does not clear it. `workflow stored list` and `get` show
`failure_paused` independently of `enabled`. Inspect the failure policy for
its generation, pause reason, observed counts, latest 100 pause/resume
transitions, and an advisory preview of retained work:

```sh
gregale workflow stored failure policy --app billing --name paid-invoice
gregale workflow stored failure resume --app billing --name paid-invoice \
  --expected-generation 1
```

`failure-resume` reads the preview before writing and rejects a stale
positive generation. The server records the acting account and starts a fresh
monitoring epoch so the previous failures do not immediately trip again.
Resuming re-arms schedule cursors without catch-up for the paused interval;
it does not change a manual pause. Preview counts can change concurrently.
Existing workflows continue; retained event recipients may create runs once
eligible, subject to quota, routing retry budgets and retention.

To receive a pause notification, configure an app webhook whose event filter
includes `stored_workflow.paused` (an empty all-events filter also receives it).
The existing signed webhook transport sends one logical event per pause
transition, even with concurrent evaluators. Delivery retries can repeat the
same event, so receivers should deduplicate by delivery identity. The payload
contains the app ID, stored workflow name, generation, reason `failure_threshold`,
policy version, pause time, and aggregate failure/sample counts. It does not
contain run inputs, outputs, errors, or tenant identities. Resume is recorded
in history without a separate webhook. Failure alert rules remain independent
and can be enabled alongside the pause policy.

## Next steps

- [Recover stored workflow runs](stored-workflow-recovery.md): resume failed runs, recover missed starts and paused work, and set recovery plans and policies.
