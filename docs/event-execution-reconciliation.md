# Reconcile existing event consumer execution coverage

Consumers created before the execution-outcome ledger can report
`ledger_baseline_unknown`. Request a new forward-only collection boundary:

```sh
gregale events subscription-reconcile-execution APP SUBSCRIPTION_ID
gregale --json events subscription-execution-reconciliation APP SUBSCRIPTION_ID
```

The matching API resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/execution-reconciliation`:
POST requests reconciliation and GET reads its status. POST requires deploy/write
scope and GET requires read scope. Go exposes `RequestEventExecutionReconciliation`
and `GetEventExecutionReconciliation`; Node and Python expose the generated event
service operations from the same OpenAPI contract.

Statuses are `not_requested`, `waiting_for_lineage`, `completed`, and
`not_required`. A waiting POST returns 202. Repeating the request preserves its
original timestamp. The scheduler processes ready work on its retention cadence;
restarts resume the saved request.

`unattributed_execution_present` means an app invocation lacks authoritative
ledger lineage. Such executions block completion, including unrelated app
execution sources and terminal invocations still retained for replay. The scheduler now attempts bounded authoritative lineage repair before testing
completion. A retained enqueued receipt must uniquely match the deterministic
delivery ID, root incarnation, account/app, normal async invocation shape and
event content. Missing, incompatible or competing evidence remains unresolved.
It never guesses ownership from headers, deletes evidence, reruns handlers, or
reconstructs historical outcomes. A busy app with unrelated retained executions may remain
blocked indefinitely.

`collection_started_at` becomes available when reconciliation completes or an
existing baseline is already known. An execution-health window must start at or
after this boundary before it can be complete. Loss markers, unknown outcomes,
retention, sample requirements and fresh observations still govern SLO eligibility.
For a five-minute health window, allow at least five minutes after the boundary
before checking eligibility. Reconciliation does not claim exactly-once delivery
or complete historical evidence.

## Repair progress

The same API, CLI and SDK response reports `repair_scanned`, `repair_repaired`,
`repair_unresolved`, and `repair_conflicts`. These are cumulative examined-receipt
counts for the request, not current execution totals or live blocker counts.
Existing matching root associations are scanned but not counted as newly repaired.
Use `unattributed_execution_present` and `status` to determine current readiness.

The durable cursor and counters commit with root associations. Failed database
transactions do not advance the cursor, and restarts resume the same scan.
A scheduler pass examines at most 100 receipt candidates across all repair jobs.
Jobs rotate by their last scan so an unresolved app does not monopolize repair.
Scans move forward through retained enqueued receipts; absent or ambiguous
historical evidence is not inferred or automatically rewritten on a later retry.
Normal new admissions continue associating their own roots transactionally.

Repair records lineage only. It does not backfill prior attempt/completion
outcomes, clear loss markers or establish historical completeness. Mapped active
roots and their replay descendants record subsequent outcomes through the existing
ledger transitions. Reconciliation still establishes a new forward-only boundary
before future SLO windows can qualify.

## Inspect and retry individual cases

```sh
gregale --json events subscription-lineage-cases APP SUBSCRIPTION_ID --limit 50
# Continue with the returned next_after value:
gregale --json events subscription-lineage-cases APP SUBSCRIPTION_ID --after 123
# Review a case, then choose a new UUID for this deliberate retry:
gregale --json events subscription-lineage-retry APP SUBSCRIPTION_ID OUTBOX_ID --request-id UUID --expected-revision REVISION
```

The API uses GET on `execution-reconciliation/cases` and POST on
`execution-reconciliation/cases/{outboxID}/retry`. Retry bodies contain
`request_id` and `expected_revision`. GET requires read scope, POST deploy/write
scope. Go exposes `ListEventExecutionLineageCases` and
`RetryEventExecutionLineageCase`; Node and Python expose matching generated event
service operations.

Case reasons distinguish invalid or missing receipts/membership, missing roots,
incompatible executions, mismatched event content, competing acceptances,
conflicting lineage and changed root incarnations. Only codes and identifiers are
returned. Cases describe their last recorded evaluation; they are not current
execution status, and earlier aggregate-only scans have no individual records.
Listings cover retained recorded cases and can change between pages.

Reuse the same request UUID **and** expected revision when a retry response is
lost. The server returns its saved result without re-evaluating or incrementing
retry counts. A stale revision or UUID reused with another guard returns 409.
Refresh the case list before deliberately choosing a new request UUID and guard.
The original receipt acceptance and any known root incarnation stay fenced.

Cases and request results are retained for at least 30 days after evaluation or
retry, independently of source receipt retention. Expired cases return 404.
Expired request results can no longer be replayed, but stale revisions prevent
repeating the old evaluation. A successful retry records lineage only: it never
reruns a handler, resets the completed scan cursor, backfills historical outcomes
or changes an established coverage boundary. Scan counters remain cumulative
first-scan evidence; use case revision and retry count for explicit retries.

## Retry a reviewed batch

Preview retained unresolved/conflicting cases; `--reason` optionally matches one
exact reason code. Pagination remains a live listing, not a reservation:

```sh
gregale --json events subscription-lineage-retry-preview APP SUB --reason missing_root --limit 50
```

Export one bounded preview page directly to a new request file:

```sh
gregale events subscription-lineage-retry-export APP SUB --reason missing_root --limit 200 --file retry-request.json
```

The command reads the existing preview API and creates no retry job. It copies
each case's revision, original
acceptance timestamp and diagnostic ID (when present), generates a request UUID,
and saves an indented JSON file with owner-only permissions. It refuses to
replace an existing file or export an empty selection. The summary identifies
the app/subscription, selected count, reason counts and any next-page cursor;
`--json` returns that summary. Only this page is selected: use `--after` and a
separate file to review another page. Preview pagination is live, and submission
still validates every guard against current state.

Review the file and submit it using the create command below. Keep the same file
and request UUID after a lost response or timeout. Exporting again creates a new
request identity; use a new export only for a deliberate new selection. The
request file contains the API body, so supply the same APP and SUB when submitting.

Alternatively, create a JSON request containing a new UUID and the exact reviewed identities:

```json
{
  "request_id": "11111111-1111-4111-8111-111111111111",
  "cases": [
    {"outbox_id": 123, "expected_revision": 1, "accepted_at": "2026-10-13T10:00:00Z"}
  ]
}
```

Use actual `outbox_id`, `revision` and `accepted_at` values from the preview.
Submitting up to 200 unique cases validates every guard before creating the job.
A stale guard returns 409; an unavailable case returns 404. Refresh and review
before changing the selection. Repeating the same retained UUID with the same
selection (in any order) returns current progress, including after a lost response.
Reusing that UUID with a different selection returns 409.

```sh
gregale --json events subscription-lineage-retry-job-create APP SUB --file retry-request.json
gregale --json events subscription-lineage-retry-job APP SUB REQUEST_UUID
```

The API uses GET `execution-reconciliation/retry-preview`, POST
`execution-reconciliation/retry-jobs`, and GET
`execution-reconciliation/retry-jobs/{requestID}`. Reads require read scope;
submission requires deploy/write scope. Go exposes
`PreviewEventExecutionLineageRetries`, `CreateEventExecutionLineageRetryJob` and
`GetEventExecutionLineageRetryJob`, with matching Node and Python operations.
POST returns 202 while pending or 200 for a previously completed job.

The scheduler evaluates at most 100 selected cases across jobs per normal retention
sweep. Each evaluation commits together with its durable progress checkpoint;
restarts resume the saved selection. A changed case becomes `changed`, and an
expired/missing case or removed subscription becomes `unavailable`. Other items
carry the recorded retry result (`repaired`, `already_associated`, `unresolved` or
`conflict`). These are recorded evaluations, not live case status. `completed`
means all selected items were processed; inspect individual outcomes for success.
Unexpected database errors roll back the item and are retried by a later sweep.

Jobs expire 30 days after creation. Expired jobs are unavailable before physical
cleanup; use a new UUID and refreshed guards after expiry. Case and receipt
retention can expire independently while a job is pending. Original receipt
acceptance and known root incarnation remain fenced. Bulk retries record lineage
only and preserve scan counters, history-loss markers and established coverage
boundaries. They never rerun handlers or recreate missing historical evidence.

## Cancel a bulk retry job

Read the retained job and copy its original `created_at`, then cancel with that
exact timestamp:

```sh
gregale --json events subscription-lineage-retry-job APP SUB REQUEST_UUID
gregale --json events subscription-lineage-retry-job-cancel APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
```

The API uses POST `execution-reconciliation/retry-jobs/{requestID}/cancel` with:

```json
{"expected_created_at": "2026-10-13T10:00:00Z"}
```

Use the actual original creation timestamp from the reviewed job, not the example.
Cancellation requires deploy/write scope and returns 200 with the terminal job.
Go exposes `CancelEventExecutionLineageRetryJob`, with matching Node and Python
operations. The creation guard prevents a delayed cancellation from targeting a
replacement after expiry and request-UUID reuse. A mismatched guard returns 409
with code `event_lineage_retry_job_changed`; missing/expired jobs return 404.
For jobs created before cancellation support, obtain the timestamp from GET.

Cancellation waits for the same job lock as scheduler evaluations. An evaluation
already holding the lock may commit before cancellation takes effect. Once
cancellation commits, further evaluations for this job stop. The job becomes
`cancelled`, and each remaining item becomes `cancelled` with reason
`job_cancelled`. Committed results and the fixed selection remain inspectable.
`processed` still counts only evaluated/skipped items; cancellation does not
increment it or mutate unprocessed cases. Other jobs and individual retries remain
independent.

Repeat cancellation with the same UUID and creation timestamp after a lost
response or timeout. A repeated cancellation preserves the first cancellation's
status, results, timestamp and retention deadline. Cancelling a completed job
returns its existing completed result. Repeating the original job submission also
returns the cancelled job with 200; it never resumes it. To retry deliberately,
review current case guards and submit a new UUID.

Cancellation leaves existing lineage repairs applied and does not extend the
30-day job retention deadline, execute or interrupt handlers, reset coverage or
change scan/history evidence. Apply the cancellation state migration before
running the updated API. Downgrading requires physically pruning cancelled job
rows first; the old CHECK rejects them without restarting work.

## Backfill missing diagnostics for older scans

Older aggregate-only repair scans have counters but no individual cases. A
separate diagnostic scan can create missing unresolved/conflicting case records
from retained ordinary enqueued receipts:

```sh
gregale --json events subscription-lineage-case-backfill APP SUB
gregale --json events subscription-lineage-case-backfill-request APP SUB --request-id NEW_UUID
```

The API uses GET/POST `execution-reconciliation/case-backfill`. Reads require read
scope; requests require deploy/write scope. Go exposes
`GetEventExecutionLineageCaseBackfill` and
`RequestEventExecutionLineageCaseBackfill`, with matching Node and Python operations.
POST accepts `request_id` and optional `expected_previous_request_id`. It requires
an existing execution coverage row from admissions or reconciliation; absent
coverage returns 404 without altering health baselines. GET returns `not_requested`
when no diagnostic request exists.

The request captures an upper retained receipt ID and persists its own cursor.
The scheduler examines at most 100 receipt candidates across diagnostic scans per
normal sweep. New case rows and progress commit together; restarts resume the
checkpoint. Existing cases are left unchanged, including their revisions and
retention deadlines. Rows awaiting physical expiry cleanup are skipped too.
Authoritatively associated admissions are counted without creating a gap case,
even if their invocation has expired. A proven root missing its association is
recorded as unresolved with reason `lineage_association_missing`; use the existing
guarded retry workflow after reviewing that case.

Status becomes `waiting_for_retry_jobs` while a retained pending bulk lineage
retry job exists for this subscription. Its queued case guards remain protected;
once those jobs finish, are cancelled or expire, scanning can continue. The
30-day diagnostic work deadline still applies while waiting. `expired` is visible
immediately at that deadline. The latest small scan metadata remains available
while its coverage row exists; there is no history of superseded scan requests.

`scanned` is the number of examined receipt candidates. It equals `recorded` plus
`existing` plus `associated`; `recorded` equals `unresolved` plus `conflicts`.
These counters describe observations and newly inserted cases, not current
execution readiness or complete history. The upper ID is fixed, but receipt
membership and retention remain live. Membership added on an already-passed ID,
late-committing receipts, or evidence removed during the scan can be missed. A
completed scan means its retained range was traversed, not that all historical
gaps are known or resolved.

Reuse the same UUID and original previous-request guard after a lost response.
The server returns current progress. To deliberately rescan after completion or
expiry, review the latest request UUID and use a new UUID with that guard:

```sh
gregale --json events subscription-lineage-case-backfill-request APP SUB --request-id NEW_UUID --expected-previous-request-id LATEST_UUID
```

Replacing an active scan or submitting a stale guard returns 409 with code
`event_lineage_case_backfill_changed`. Superseded requests do not restart work.
After expired cases are physically pruned, a later scan can create fresh records
from retained evidence. Review the current case and choose a new retry UUID;
old diagnostic identities fail against that fresh lifetime, even if the numeric
revision repeats.

Diagnostic scans never associate roots, invoke handlers, backfill execution
outcomes, overwrite existing case records, move the forward repair cursor/counters,
clear loss markers or alter collection boundaries. Lost source evidence stays
unavailable. Apply the diagnostic-ID migration and deploy the updated API and scheduler writers
together before requesting scans; older workers can discard JSON fields they do
not understand, and older API binaries do not enforce diagnostic-ID guards.

Newly backfilled cases include a unique `diagnostic_id`. Copy it into
`expected_diagnostic_id` for both individual retry bodies and bulk job selections.
The CLI supports this guard for individual retries:

```sh
gregale --json events subscription-lineage-retry APP SUB OUTBOX_ID --request-id NEW_UUID --expected-revision CURRENT_REVISION --expected-diagnostic-id CURRENT_DIAGNOSTIC_ID
```

For a bulk request, add `"expected_diagnostic_id": "CURRENT_DIAGNOSTIC_ID"` to each
selection whose reviewed case has `diagnostic_id`. Use the actual UUID, not the
placeholder. Missing or changed identities return 409 before evaluation. Repeating
an individual retry or bulk submission must preserve this guard along with the
other original fields. Existing legacy cases without a diagnostic ID keep their
previous request contract. Older SDKs can still retry those legacy cases; upgrade
or include the new field explicitly when retrying backfilled diagnostics.

A recreated diagnostic case receives a fresh identity after pruning. This prevents
an old retry from matching a reset numeric revision or a changed root incarnation.
Case listing and retry results expose identifiers and reason codes only; no event
payload is added. Downgrading the column migration requires stopping diagnostic
requests and physically pruning guarded cases and retry jobs first.

## Control diagnostic scans

Read the latest scan and copy its `request_id` and exact original `created_at`:

```sh
gregale --json events subscription-lineage-case-backfill APP SUB
gregale --json events subscription-lineage-case-backfill-pause APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
gregale --json events subscription-lineage-case-backfill-resume APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
gregale --json events subscription-lineage-case-backfill-cancel APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
```

POST `execution-reconciliation/case-backfill/{requestID}/pause`, `/resume` or
`/cancel` with `{"expected_created_at": "ORIGINAL_CREATED_AT"}`, substituting the
actual timestamp from GET. Each requires deploy/write scope and MFA and returns
200 with current scan metadata. Go exposes `PauseEventExecutionLineageCaseBackfill`,
`ResumeEventExecutionLineageCaseBackfill` and
`CancelEventExecutionLineageCaseBackfill`; Node and Python expose matching operations.
A wrong/superseded UUID or creation timestamp returns 409 with
`event_lineage_case_backfill_changed`. Missing scan/access returns 404.

Pause preserves progress and excludes the scan from scheduler selection. Resume
continues the same cursor and fixed upper receipt ID; active bulk retry jobs may
still cause `waiting_for_retry_jobs`. Paused scans cannot be replaced until cancelled
or expired, and their original 30-day deadline continues running. Resume after
expiry returns 409. Source evidence can expire or change during the pause.

Cancel stops future diagnostic batches after it commits. It preserves the cases
already created, their diagnostic identities, counters and retention. An already
running scheduler batch may commit before pause/cancel gets the coverage lock.
Normal repair and separately authorized retries/deliveries continue independently.
No control repairs roots, executes handlers, resets repair evidence or extends a
work/case deadline.

Repeating pause on paused, or resume on pending/waiting, leaves metadata unchanged.
Repeating cancel on cancelled, completed or expired returns that terminal state
unchanged. Completed/cancelled/expired scans reject pause and resume with 409.
A cancelled scan cannot resume. Repeating its original request also leaves it
cancelled; to deliberately rescan, choose a new UUID and pass the cancelled UUID
as `expected_previous_request_id`.

These are desired-state controls. Opposing requests take effect in server lock
order, and the creation guard does not order separate control commands. After a
lost response, timeout or another operator's opposite control, read status before
repeating your desired action. A delayed repeated pause/resume can reapply its
state after an intervening opposite operation; cancellation remains terminal.
