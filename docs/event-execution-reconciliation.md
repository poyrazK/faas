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
each case's revision, original acceptance timestamp and diagnostic ID (when
present), generates a request UUID, and saves a versioned scoped file with
owner-only permissions. The file includes the app and subscription UUIDs. It refuses to
replace an existing file or export an empty selection. The summary identifies
the app/subscription, selected count, reason counts and any next-page cursor;
`--json` returns that summary. Only this page is selected: use `--after` and a
separate file to review another page. Preview pagination is live, and submission
still validates every guard against current state.

Review the file and submit it using the create command below. Keep the same file
and request UUID after a lost response or timeout. Exporting again creates a new
request identity; use a new export only for a deliberate new selection. The
scoped file includes its target, which create and preflight validate before use.
Pass `--raw` to export a legacy API-body file for older clients or direct SDK use.

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
POST returns 202 while pending or 200 for a previously paused, completed or
cancelled job. Repeating submission never resumes paused work.

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

## Scope-bound retry files

Both preview export and finished-job export now default to a versioned scoped
file. It binds the saved selection to a stable app UUID and subscription UUID:

```json
{
  "format": "gregale.event-lineage-retry",
  "version": 1,
  "app_id": "22222222-2222-4222-8222-222222222222",
  "subscription_id": "33333333-3333-4333-8333-333333333333",
  "request": {
    "request_id": "11111111-1111-4111-8111-111111111111",
    "cases": [
      {"outbox_id": 123, "expected_revision": 1, "accepted_at": "2026-10-13T10:00:00Z"}
    ]
  }
}
```

Finished-job exports add `source_job` with the reviewed source `request_id` and
original `created_at`. This is retained local provenance; submission does not
require the source job to remain available. Preview exports omit it. Export
summaries identify the format and stable app UUID; preflight reports include
`scope` and any `source_job` metadata.

Use the same create and preflight commands with these files. A different
subscription is rejected locally before authentication or API requests. For the
app, the CLI reads metadata for the supplied slug and compares its stable UUID
before case checks or submission. App renames are supported when the supplied
slug resolves to the same ID. This metadata read requires read scope in addition
to the existing deploy/write permission for create. Preflight pins subsequent
job/case observations to the file's app identity too. Scope mismatches return
exit 1, leave the file untouched and never submit a job.

Only the nested `request` is sent to the unchanged API; scope and provenance
remain local. The CLI rejects unsupported formats/versions, missing required
scope IDs, malformed request guards, unknown bundle fields and duplicate field
names (including alternate casing). Files containing reserved bundle keys (`format`, `version`,
`app_id`, `subscription_id`, `request` or `source_job`) are handled as bundles;
an invalid bundle never falls back to an unscoped body. File size remains capped
at the existing 64 KiB request-file limit. Exports retain owner-only permissions,
exclusive creation and cleanup of incomplete writes.

Existing raw API-body files remain readable by create and preflight. Add `--raw`
to either exporter when an older CLI or a direct API/SDK consumer needs that
format. Raw files carry no target binding or source provenance, so the operator
must supply the intended APP and SUB. Scoped files require the updated CLI, or
an integration that validates their target and extracts `request` before calling
the API. Files are editable local artifacts; server authorization, ownership,
current guard checks and retained-request idempotency remain authoritative.

## Preflight a saved retry request

Check a reviewed export before submission, preserving the original file and UUID:

```sh
gregale --json events subscription-lineage-retry-preflight APP SUB --file retry-request.json
```

The command validates the bounded file and its nonzero request UUID, 1–200 unique
positive outbox IDs, positive revisions, original acceptance timestamps and any
nonzero diagnostic UUIDs before calling read APIs. UUID casing, selection order
and timestamp zones are normalized in memory using submission semantics. The
file's contents, request UUID and guards are never rewritten.

Each case uses a one-row current listing at its outbox ID. Reports contain the
original selection, current case metadata when available, a status and all
observed issues: `acceptance_changed`, `diagnostic_changed`, `revision_changed`,
`no_longer_retryable` or `unavailable`. `ready` means the current case is unresolved
or conflicting with all supplied guards matching. Missing a required diagnostic
guard is reported as `diagnostic_changed`. When several checks fail, `status` is
the first issue in the above order and `issues` preserves every mismatch.

Preflight also reads the retained request UUID before and after checking cases:

- `already_submitted`: a retained job has the same normalized selection. A repeat
  submission returns this existing job, even if current case guards have changed.
- `identity_conflict`: the retained UUID belongs to another selection.
- `identity_changed`: its creation/app/retention identity changed during the check.
- `identity_unavailable`: an initially retained job became unavailable.
- `not_retained`: neither request lookup observed a retained job.
- `unverified`: the request identity check did not finish successfully.

The JSON report includes `request_state`, any `existing_job` summary, per-case
results, `total`, `checked`, `ready_cases`, `complete` and `ready`. A complete report
is ready when all case guards match and no conflicting/changing request identity
was observed, or when the same selection is already submitted. Thus an
`already_submitted` report can be ready while individual current cases report
issues; it does not request a second evaluation or claim that repair succeeded.

Exit 0 requires a complete, ready report. A complete report with stale/unavailable
cases or a conflicting request identity returns 1. API/transport errors and
malformed responses emit an incomplete report with `complete: false` and
`ready: false`, then a diagnostic on stderr with the normal CLI error code.
Timeout returns 3 and Ctrl-C 130. `--timeout` defaults to 2 minutes and must be
positive; individual reads also have the existing control timeout. At most 202
read requests are made for a full 200-case raw file, or 203 for a scoped file
including its app-identity check; no unbounded case scan occurs.

Preflight is advisory and read-only. Reads are live observations, not a
transaction, reservation or guarantee that submission will succeed. A 404 request
lookup cannot distinguish a fresh UUID from an expired identity that remains
physically retained and blocks reuse until pruning. Retention can expire and
case guards can change after checking. Preflight does not verify deploy/write
permissions or reserve capacity. Submission remains the authoritative atomic
validation and idempotency check. Review mismatches and deliberately export a new
request if needed; do not silently refresh guards under an already-used UUID.

## Find retained bulk retry jobs

List jobs when their request UUID is unknown or another operator submitted them:

```sh
gregale --json events subscription-lineage-retry-jobs APP SUB --limit 50
gregale --json events subscription-lineage-retry-jobs APP SUB --status pending --limit 50
# Continue with the returned next_after, keeping the same status filter:
gregale --json events subscription-lineage-retry-jobs APP SUB --status pending --after CURSOR
```

The API uses GET `execution-reconciliation/retry-jobs` with optional `status`,
`limit` and `after`. Read scope and MFA apply. Status accepts `pending`, `paused`,
`completed` or `cancelled`; omit it for all retained jobs. The default limit is 50 (zero also
selects 50), maximum 200. The response contains `jobs` and, when another page was
observed, `next_after`. An existing subscription with no retained jobs returns
`jobs: []`, including before execution coverage exists. Unknown or inaccessible subscription
targets return 404. Existing retained delivery-control identities remain accessible
after removal of the subscription declaration. Expired jobs are unavailable even before physical pruning.

Summaries include request/subscription/app identities, selected and processed
counts, original creation/update timestamps and expiry. They omit case selections
and results. Processed counts evaluated/skipped items, not items cancelled without
evaluation; a completed job is not proof that every case was repaired. Use the
existing individual GET command with a discovered UUID to review the full job.
For cancellation, obtain the original `created_at` from that review.

Jobs are ordered newest first by creation timestamp, then descending request UUID
for ties. Pass the returned cursor unchanged; it remains usable if its boundary
job expires. Pagination is live: progress/status can change, expiry removes jobs,
and new jobs or jobs entering a filtered state above the cursor require a fresh
listing. A late-committed creation may also appear above a passed cursor. Listing
does not extend retention, reserve jobs or provide historical audit completeness.

Go exposes `ListEventExecutionLineageRetryJobs`; Node exposes
`EventsService.listEventExecutionLineageRetryJobs`; Python exposes
`faas_sdk.preview.api.events.list_event_execution_lineage_retry_jobs` with sync and async
operations. Apply the listing-index migration before enabling this API at scale.

## Watch a bulk retry job

Monitor a discovered or submitted request without changing its work:

```sh
gregale events subscription-lineage-retry-job-watch APP SUB REQUEST_UUID --timeout 10m --poll-interval 2s
# Optionally fence the first read to the incarnation you reviewed:
gregale --json events subscription-lineage-retry-job-watch APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
```

The watcher uses the existing individual GET API and requires read scope. It
reads immediately, then delays between reads (default 2 seconds, allowed 1 second
to 1 hour). The total watch timeout defaults to 10 minutes and must be positive;
each read also has the existing subscription-control timeout. Watching continues
through paused state until a terminal state, unavailability or the original watch
deadline. Progress is emitted only when job state, processed count or case outcome
counts change. Summaries
include pending, repaired, already-associated, unresolved, conflicting, changed,
unavailable and cancelled cases. These are recorded results, not live case status.

`--json` emits one compact JSON object per line. Records contain `event`, app,
subscription/request IDs and the last observed `job` summary when available.
Events are `progress`, `completed`, `cancelled` or `stopped`; stopped records carry
`stop_reason` (`timeout`, `interrupted`, `unavailable`, `job_changed`,
`invalid_response` or `read_failed`). Errors are reported separately on stderr.

Completed returns exit 0, even when recorded cases remain unresolved/conflicting
or changed/unavailable; inspect counts before deciding whether more repair is
needed. Cancelled and locally invalid responses return exit 1; unavailable (404)
returns 4. Other API and transport errors retain the existing CLI exit codes
(including 3 for timeout/transport failures, 2 for authentication and 6 for
forbidden access). Ctrl-C returns 130. A 404 means the job or subscription is
unavailable, including expiry or lost access, and does not prove the job completed or was cancelled.
After monitoring stops, the last observed counts can be stale. Read errors stop
the watcher rather than silently retrying; restart it with the same request UUID.

The first successful read pins original creation time, app identity and retention
for the watch lifetime. A later response with a different incarnation stops
monitoring. `--expected-created-at` optionally pins the first read too; use the
original timestamp from the reviewed job. Without that guard a fresh invocation
can observe a replacement UUID after physical expiry/pruning. Timeout or Ctrl-C
stops monitoring only; it never submits retries, cancels jobs or changes retention.

## Export remaining cases from a finished job

Read the source job, review its outcomes and copy its original `created_at`, then
prepare another request with current guards:

```sh
gregale --json events subscription-lineage-retry-job-export APP SUB SOURCE_UUID --expected-created-at ORIGINAL_CREATED_AT --file next-retry.json
# Select particular recorded outcomes, including unevaluated cancelled items:
gregale events subscription-lineage-retry-job-export APP SUB SOURCE_UUID --expected-created-at ORIGINAL_CREATED_AT --outcomes unresolved,changed,cancelled --file next-retry.json
```

The source must be completed or cancelled; a paused source must resume or be
cancelled before exporting remaining cases. `--expected-created-at` is required
and fences its original incarnation on both the initial read and the final read
before writing. Pending, expired, missing, inaccessible or replaced source jobs
produce no file. Export only uses existing read APIs; it does not evaluate cases,
submit another job or change either job's retention.

`--outcomes` selects recorded source-item states, defaulting to
`unresolved,conflict,changed,unavailable`. `cancelled` is also allowed explicitly;
`pending`, `repaired` and `already_associated` are rejected. Whitespace and
repeated selections are normalized. Filtering concerns the original result;
a selected unresolved case can now be conflicting, or already repaired elsewhere.

For each selected item, seek a one-row current case page at its original outbox
ID. Include only a current unresolved/conflicting case with the same original
receipt acceptance and diagnostic incarnation. Copy its latest revision,
acceptance timestamp and diagnostic ID into the new request. Report skipped items
with original outcome and reason: `unavailable` (no retained case),
`no_longer_retryable` (now repaired/already-associated), `acceptance_changed` or
`diagnostic_changed`. A recreated diagnostic is deliberately excluded; review it
through the regular preview/export workflow. Legacy cases without diagnostic IDs
retain their existing acceptance/revision contract and cannot independently prove
that a diagnostic-free case was not recreated.

The JSON summary contains source UUID/creation time, destination file/new UUID,
app/subscription, selected count, selected source-outcome counts, current reason
counts and skipped items. The default request file also includes target UUIDs and
source-job provenance. Pass `--raw` for the legacy API body without those fields.
Cases appear once in ascending outbox order. If none remain retryable, print the
summary and exit 1 without creating a file or request UUID. Read failures,
malformed responses, timeout or Ctrl-C during the read phase abort before writing
a request. File write failures remove the incomplete destination.
Read errors retain normal CLI exit codes; Ctrl-C returns 130.

The export is bounded by the source job's maximum 200 items. It performs two job
reads and at most one case read per selected item; it never walks all subscription
cases. `--timeout` defaults to 2 minutes and must be positive; individual reads
also use the existing control timeout. Reads are live observations, not an atomic
snapshot or reservation. Guards can change after export and submission will still
reject stale guards. Source expiry can race with completion of the final read.

A successful export uses a fresh request UUID and writes a new owner-only file,
refusing existing destinations. Review the file and summary, then submit through
`subscription-lineage-retry-job-create` with the same APP and SUB. Preserve this
file and UUID for retries after a lost submission response; another export is a
new deliberate request identity.

## Pause or resume bulk retry work

Read the job and copy its original `created_at`, then control that incarnation:

```sh
gregale events subscription-lineage-retry-job-pause APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
gregale events subscription-lineage-retry-job-resume APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT
gregale --json events subscription-lineage-retry-jobs APP SUB --status paused
```

The API uses POST `execution-reconciliation/retry-jobs/{requestID}/pause` and
`/resume` with `{"expected_created_at": "ORIGINAL_CREATED_AT"}`. Both require
existing deploy/write scope and MFA, returning 200 with current job progress.
Go exposes `PauseEventExecutionLineageRetryJob` and
`ResumeEventExecutionLineageRetryJob`; Node and Python expose corresponding events
operations. Controls wait for the worker's job lock and recheck expiry after any
lock wait. Missing/expired jobs return 404. A different original creation timestamp
or a completed/cancelled job returns 409, code `event_lineage_retry_job_changed`.

Pause changes pending to paused. An evaluation already holding the lock may commit
before pause; after pause commits, no new evaluation for that job starts. Resume
changes paused to pending and preserves the saved selection, committed per-case
results, pending items, processed count and original expiry. Evaluation continues
at the next unprocessed item with the same guards. Other repairs can change those
cases while paused, so resumed work may report changed or unavailable items.
Pause does not freeze evidence or extend the 30-day retention deadline.

Repeated pause on paused or resume on pending preserves state and update time.
Repeating original submission also returns paused progress without resuming it.
Cancellation accepts pending or paused jobs and retains its original creation guard
and committed results. Watchers continue through paused state; matching saved
requests remain `already_submitted` in preflight. Paused jobs count as unfinished
bulk work, so diagnostic case scans still wait until those jobs finish, are
cancelled or expire. Normal forward repair and delivery execution remain independent.

Without a command UUID these remain desired-state controls. Conflicting
pause/resume actions follow server lock order; a delayed repeated action can reapply after an opposite action. After
a timeout or lost response, read current state before repeating the desired control
with the original UUID and creation timestamp. Cancelled jobs remain terminal.

Apply the state-check migration and upgrade API/scheduler writers before enabling
controls. Older CLI/SDK versions may reject paused states. Downgrade deliberately
fails while paused rows remain: explicitly resume/cancel them or expire and prune
them first. The migration does not resume work on its own.

## Repeat a control safely after a lost response

Choose a fresh command UUID for each deliberate pause/resume/cancel action and preserve
it with the job's original creation guard:

```sh
gregale events subscription-lineage-retry-job-pause APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --command-id PAUSE_COMMAND_UUID
gregale events subscription-lineage-retry-job-resume APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --command-id RESUME_COMMAND_UUID
gregale events subscription-lineage-retry-job-cancel APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --command-id CANCEL_COMMAND_UUID
# A repeat of the original pause UUID acknowledges that command without pausing again:
gregale --json events subscription-lineage-retry-job-pause APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --command-id PAUSE_COMMAND_UUID
```

API bodies add optional `command_id` alongside `expected_created_at`. Nonempty
command IDs must be nonzero UUIDs; casing is normalized. A UUID is scoped to this
subscription/job incarnation. Reuse it only with its original action and creation
guard. An opposite action returns 409 `event_lineage_retry_control_command_changed`;
a stale job creation guard retains 409 `event_lineage_retry_job_changed`.

State changes and the successful command receipt commit atomically under the
worker's job lock. A repeat looks up the receipt before attempting another state
transition, including when the job has since resumed, completed or been cancelled.
The response's outer job fields are current observations. Its `control_receipt`
is the original immutable command outcome, with action, sequence, result_status,
selected/processed counts, observed job update time, recording time, target IDs,
creation guard and expiry. A repeated pause may therefore return current job status
`pending` with receipt result_status `paused`. Inspect these fields separately.
The receipt contains metadata only and is not saved in the current job snapshot.

At most 128 receipts are retained per job. Successful new-UUID no-ops also record
a receipt and consume a slot; repeats of an existing UUID do not. New UUIDs beyond
the cap return 429 `event_lineage_retry_control_limit` without changing the job.
Existing UUID repeats and controls without command IDs remain available, including
cancellation. A new UUID-keyed cancellation shares the cap and returns 429 when
full; omit command_id to cancel without recording another receipt. Failed
requests create no receipt. Receipts expire with the job's unchanged original
retention deadline and are deleted when that job is pruned. Old creation guards
prevent commands targeting a replacement after physical pruning and UUID reuse.

New receipts also include server-derived `actor_kind`, `actor_id` and
`credential_type`. Sessions record the authenticated account UUID (`account`,
`session`); bearer calls record the credential UUID (`credential`) and its type
(`api_key`, `deploy_token` or `platform_tenant_token`). These are identity metadata,
not token values, token hashes, session IDs or emails. Attribution is not accepted
in control request bodies. Session attribution identifies the account, not a
particular session or separately identified human.

A repeat from another authorized credential preserves the original actor. Actor
identity does not change the command conflict key or confer authorization. Revoked
credentials retain their historical identity in receipts until normal job pruning.
Older receipts omit these fields and CLI output labels them unknown; they are not
backfilled. Direct internal store calls without authenticated principal context
record `internal` / `store` / `internal`. Attribution is stored in the existing
JSON result, so no schema migration is required. Upgrade all API writers to ensure
consistent attribution of new receipts; older writers may still omit it during a
rolling upgrade. Legacy controls without command UUIDs and failed requests are
still absent, so this history is not a complete actor audit.

To inspect retained receipts:

```sh
gregale --json events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --limit 50
# Continue with the returned sequence cursor:
gregale --json events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --after NEXT_AFTER
```

GET `execution-reconciliation/retry-jobs/{requestID}/controls` requires read scope,
MFA and query `expected_created_at`. The default limit is 50 (zero also selects the
default), maximum 200. `after` is an exclusive sequence, default 0. Receipts appear
in ascending per-job sequence, assigned under the job lock; timestamps are
informational and do not determine ordering. The response has `controls` and,
when another page was observed, `next_after`. A retained job with no UUID-keyed
commands returns an empty array. Missing/expired jobs return 404 and stale creation
guards 409. Pagination observes live appended receipts and does not reserve future
commands or extend retention. Legacy controls without command IDs and failed
requests are absent; this is not a complete historical audit.

History accepts optional exact `action` (`pause`, `resume`, `cancel`), `actor_id`
(nonzero hyphenated UUID, casing normalized, or `store` for internal calls), and
`credential_type` (`session`, `api_key`, `deploy_token`, `platform_tenant_token`,
`internal`). Empty filters mean all values; nonempty filters combine with AND
before the page limit. Invalid filters return 400. These identify the original
recorded command actor, independently of the authorized caller reading history.
Older unattributed receipts remain visible without attribution filters and can
match an action filter; they cannot match nonempty actor or credential filters.

```sh
gregale --json events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --action cancel --credential-type api_key
gregale --json events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --action cancel --credential-type api_key --after NEXT_AFTER
# Start a separate selection from the beginning:
gregale --json events subscription-lineage-retry-job-controls APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --actor-id ORIGINAL_ACTOR_UUID
```

Cursors always refer to original per-job sequences, so gaps in filtered results
are normal. `next_after` exists only when another matching receipt was observed.
Keep the same filters on every page; restart at `after=0` when changing filters to
inspect the complete new selection. A matching page can be empty even though the
job has other receipts. Filtering does not freeze the live append-only history,
reserve future matches or change retention. No schema migration is required.
The Go method accepts an optional `EventExecutionLineageRetryJobControlFilters`
argument (at most one); existing calls without it retain unfiltered behavior.
Node/Python operations accept matching optional query parameters.

Go exposes `ListEventExecutionLineageRetryJobControls`; Node and Python expose the
corresponding events operation. Pause/resume/cancel SDK bodies accept command_id and job
responses expose optional control_receipt. Without command_id, the previous
response shape and desired-state semantics remain. After a timeout with a command
UUID, repeat the same UUID/action/guard; without one, read current state first.
Apply the receipt migration and upgrade API writers before using this field.
Downgrade refuses while receipts remain; stop UUID-keyed controls and expire/prune
their parent jobs first to avoid discarding live deduplication evidence.

Cancellation receipts use the same journal and history endpoint. A new cancel
command on an already completed or cancelled job records an acknowledgment of
that terminal state, preserving the update time and evaluated count. The receipt
result_status can therefore be completed or cancelled. New pause/resume commands
on terminal jobs still return 409. Failed cancellations record no receipt.
Apply the cancellation-receipt action migration before API writers accept this
field. Upgrade SDKs that use closed receipt action/state enums before reading
cancellation history. Downgrade refuses while cancellation receipts remain; stop
UUID-keyed cancellations and expire/prune their parent jobs first.

## Export control history for an incident review

```sh
gregale --json events subscription-lineage-retry-job-controls-export APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --file controls.json
# Select receipts from the same bounded observation:
gregale --json events subscription-lineage-retry-job-controls-export APP SUB REQUEST_UUID --expected-created-at ORIGINAL_CREATED_AT --file cancellations.json --action cancel --credential-type api_key
```

The export requires read scope and MFA and does not mutate the job or replay any
command. It first reads the retained job to pin app/subscription/job identity and
the reviewed creation timestamp, then fetches its entire unfiltered control
journal in one guarded GET. The maximum 128 receipts fit in one page; filters are
applied locally after validating the response. No paging loop can mix commands
from different observations. The command rejects incomplete pages, invalid
sequences, duplicate commands, scope or incarnation mismatches, and malformed
receipt outcomes or attribution before publishing a file.

`controls.json` is a version-1 `gregale.event-lineage-control-history` artifact.
It contains app slug and stable app/subscription/job IDs, `expected_created_at`,
`job_retain_until`, local `observed_at`, normalized `filters`, `observed_receipts`,
`through_sequence`, and the selected `controls` array. The sequence boundary and
observed count describe the full unfiltered history read, even if filters match
nothing. Receipt sequences remain original sequences, so filtered output can have
gaps. Empty histories and empty matching sets produce a valid artifact with an
empty array; an empty history has `through_sequence: 0`.

Original actor attribution is preserved, including absent fields on legacy
receipts. No event payloads, case selections/results, token values or session IDs
are exported. The file is unsigned and editable; it is an observation for review,
not a replay request or proof of tamper resistance. Later commands, previously
expired jobs/receipts, legacy controls without UUIDs and failed requests are
outside this export. The initial job read and history read are separate requests;
job state may change between them, and the artifact deliberately omits that state.
Server retention is not extended; a saved artifact can outlive the server copy.

The encoded JSON is bounded to 256 KiB. The CLI creates a private 0600 temporary
file in the destination directory, syncs its contents and atomically publishes the
completed file without replacing an existing destination, including a symlink.
Failures remove the temporary file and never expose a partial destination. A
process crash may leave a private temporary file; the final path is always absent
or complete. Filesystems without hard-link support fail without a fallback write.
This does not guarantee the directory entry survives a filesystem crash.
Use `--timeout DURATION` for the overall bound (default `2m`, must be positive).
Each API read also uses the existing control timeout. Ctrl-C before publication
aborts; after publication the completed artifact remains saved. API errors,
unavailable/expired jobs and stale creation guards do not produce an artifact.
JSON stdout summarizes the saved file and counts; the bundle is in `--file`.
No API or schema migration is needed.

## Inspect or compare saved history offline

```sh
gregale events subscription-lineage-retry-job-controls-inspect --file controls.json
# controls-later.json is the target; controls.json is the earlier baseline:
gregale --json events subscription-lineage-retry-job-controls-inspect --file controls-later.json --compare controls.json
```

This command reads local artifacts only. No login or API access is needed, and
neither file is changed. Regular files must fit the existing 256-KiB export bound.
The inspector rejects unsupported formats/versions, missing or null fields,
unknown/case-aliased fields, duplicate keys at any level, trailing JSON, invalid
scope/retention/filter metadata, duplicate command UUIDs, invalid original
outcomes/attribution and unordered or out-of-bound sequences. Unfiltered exports
must contain every observed sequence. Filtered exports may have gaps but all
included receipts must match the recorded filters. Their omitted matching records
cannot be independently proven from one unsigned file.

The summary includes stable job scope, creation and retention, original filters,
sequence boundary, observed and selected counts, actions, original recorded
outcomes, actor identities and credential types, and a separate legacy-unattributed
count. Counts summarize selected receipts, not current job state. Past server
retention is allowed: the file remains inspectable after server pruning. Structural
validation does not authenticate a file, prove tamper resistance or establish a
complete historical audit.

For comparison, use exports from the same stable app/subscription/job incarnation,
with identical original creation/retention and normalized filters. `--compare` is
the baseline; `--file` must have an equal or greater observed sequence boundary.
Changing filters or reversing boundaries returns an error instead of inferring
missing commands. App slug hints and local observation timestamps can differ;
sequence determines order. Equivalent UUID casing/representations and timestamp
offsets are normalized only in memory.

Comparison reports unchanged receipts and added matching records beyond the
baseline boundary. It flags immutable records that changed at an existing sequence,
command UUIDs moved to another sequence, records missing from the target overlap,
and records inside the old boundary that were missing from the baseline. Filtered
exports still do not reveal later nonmatching records; `through_sequence` describes
the full observed journal, while `added` includes selected matching additions.
Valid inspection and consistent comparison exit 0. Conflicts return a comparison
report and exit 1; malformed or incomparable inputs fail without a success report.
JSON output contains the summary and optional comparison; human output lists the
same counts and conflict reasons. No API or schema migration is required.

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
Cancellation requires deploy/write scope and MFA and returns 200 with the terminal
job. Supply optional command_id (CLI --command-id) to record an immutable receipt;
preserve that command UUID alongside the original creation guard when repeating.
Go exposes `CancelEventExecutionLineageRetryJob`, with matching Node and Python
operations. The creation guard prevents a delayed cancellation from targeting a
replacement after expiry and request-UUID reuse. A mismatched guard returns 409
with code `event_lineage_retry_job_changed`; missing/expired jobs return 404.
For jobs created before cancellation support, obtain the timestamp from GET.

Cancellation waits for the same job lock as scheduler evaluations. An evaluation
already holding the lock may commit before cancellation takes effect. Once
cancellation commits, further evaluations for this job stop. A pending or paused
job becomes `cancelled`, and each remaining item becomes `cancelled` with reason
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

Status becomes `waiting_for_retry_jobs` while a retained pending or paused bulk
lineage retry job exists for this subscription. Its queued case guards remain protected;
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

## Diagnose pending and paused retry work

Consumer execution health now includes optional `lineage_retry_jobs`, also present
inside `execution` in combined delivery health. Existing read scope and MFA apply.
Use the existing commands:

```sh
gregale --json events subscription-execution-health APP SUB --window 15m
gregale events subscription-delivery-health APP SUB --window 15m
```

These diagnostics observe retained pending/paused bulk retry jobs independently
of the execution outcome window. Completed, cancelled, expired and future-created
jobs are excluded. They report job counts, pending/paused remaining cases, stalled
pending jobs, retention-risk jobs, unknown progress, oldest observed pending
activity, earliest observed expiry and up to ten metadata samples. Samples include
request UUID, original creation guard, current state/counts, activity/progress ages,
original retention deadline and warning flags. They contain no case selections,
case results, command receipts or event payloads.

A pending job becomes `stalled_pending` after at least 15 minutes without job
activity. This is an advisory warning, not proof that the scheduler failed:
capacity or other delays can also prevent progress. Deliberately paused jobs never
receive that flag. Resuming a paused job updates activity and restarts its pending
inactivity interval. Jobs with 24 hours or less until their original deadline get
`retention_risk`, including paused work. Diagnostics do not resume, retry, cancel
or extend retention; review the job before using its existing guarded controls.

`last_progress_at` is now optional on retained job responses and is recorded in
the existing JSON snapshot when an upgraded worker commits a case evaluation or
skip, including changed/unavailable cases. A failed/rolled-back evaluation records
nothing. Pause/resume/cancel controls preserve it. Progress age and activity age
are separate: before any evaluation progress age uses creation and
`last_progress_at` is absent; older evaluated snapshots without a timestamp have
`progress_known: false` and omit progress age instead of treating control time as
progress. Their next committed worker evaluation establishes evidence. Upgrade
all API writers and workers to preserve this field consistently; older writers
may omit it during a rolling upgrade. No progress timestamp is guessed or backfilled.

At most 500 active jobs are observed, ordered by earliest retention deadline then
activity and UUID. If more exist, `counts_complete: false` means counts are lower
bounds and extrema cover only the observed set. Samples prioritize retention risk
then stalled pending work within that set. Neither an empty warning count under
truncation nor an omitted sample proves all other jobs are healthy. The block's
completeness is separate from handler outcome history and execution-root truncation;
it does not change eligibility for existing execution alerts. Older API versions
may omit the entire block.

The new index-only migration supports this read; no table columns or compliance
registry entries change. Index rollback preserves job data and progress metadata.
Go, Node and Python SDK models expose the optional block and last-progress field.

## Watch retry-job lifecycle warnings

Use the existing delivery-health watcher with new opt-in thresholds:

```sh
gregale events subscription-delivery-health APP SUB --watch --lineage-stalled-threshold 1 --lineage-retention-risk-threshold 1 --lineage-paused-threshold 15m --timeout 1h --checkpoint retry-health.json --alerts-json
# Resume with the same scope, window and thresholds:
gregale events subscription-delivery-health APP SUB --watch --lineage-stalled-threshold 1 --lineage-retention-risk-threshold 1 --lineage-paused-threshold 15m --timeout 1h --checkpoint retry-health.json --resume --alerts-json
```

`--lineage-stalled-threshold N` selects the count of jobs classified stalled by
server diagnostics (pending with no job activity for 15 minutes).
`--lineage-retention-risk-threshold N` selects active jobs classified within
24 hours of original retention expiry, including paused work. Their metric names
are `lineage_retry_stalled_jobs` and `lineage_retry_retention_risk_jobs`.
`--lineage-paused-threshold DURATION` selects `lineage_retry_paused_seconds`:
consecutive locally sampled fresh observations of any nonzero paused retry backlog,
with at least two reads. It does not establish how long any particular job was
paused; the set of paused jobs may change. Count thresholds use existing positive
bounded integer limits, duration thresholds use 100ms–24h, and zero disables a
condition. These flags require `--watch`.

Evidence comes from the scoped `execution.lineage_retry_jobs` block and execution
observation timestamp. Missing blocks (including older servers), truncated counts,
malformed partitions, stale/future or nonmonotonic observations, mismatched scope,
and read gaps become unknown evidence. Even a positive truncated lower bound is
not used to confirm or resolve a retry-job condition. Handler outcome history and
execution-root truncation remain separate from this block's completeness. Only
fresh complete valid observations can open or resolve these conditions.

The existing lifecycle emits opened/resolved/confirmation_lost transitions, with
stable incident IDs and increasing alert sequences. A checkpoint retains active
incident identities and pending alerts. Resume checks identical selection and
thresholds and requires fresh evidence; count conditions reconfirm an existing
incident without reopening it, while paused duration must be re-established after
restart. A fresh complete zero can resolve it. Unknown evidence loses confirmation
rather than falsely resolving; later complete evidence can begin a new incident.
Unacknowledged durable notifications may be delivered again after restart; consumers
should acknowledge or deduplicate by the existing incident/sequence identity.
Use the existing acknowledgement inbox or checkpoint acknowledgement commands.

Go `EventDeliveryHealthWatchOptions` exposes `LineageStalled`,
`LineageRetentionRisk`, and `LineagePaused`. Existing watches/checkpoints retain
behavior when these options are omitted. Adding thresholds changes the watch
selection and requires a matching checkpoint or a new one. Older CLI/Go SDK
versions cannot resume checkpoints using the new metric names. Alerts are local
output/callbacks; no external messages or automatic recovery actions are sent.
No API/schema migration or registry update is needed.

## Investigate every matching retry job after an alert

Use the retained job listing with an attention filter to go beyond the health
endpoint's ten samples and 500-job diagnostic observation bound:

```sh
gregale --json events subscription-lineage-retry-jobs APP SUB --attention any --limit 50
# Continue with the returned next_after and identical filters:
gregale --json events subscription-lineage-retry-jobs APP SUB --attention any --limit 50 --after NEXT_AFTER
# Focus on pending inactivity or deliberately paused work approaching expiry:
gregale events subscription-lineage-retry-jobs APP SUB --attention stalled --status pending
gregale events subscription-lineage-retry-jobs APP SUB --attention retention_risk --status paused
```

The existing GET `execution-reconciliation/retry-jobs` accepts optional
`attention=any|stalled|retention_risk`. Any is the union of both warning types.
Attention includes only retained pending/paused jobs created by the observation
time. Stalled means pending with at least 15 minutes of inactivity; paused jobs
never match it. Retention risk means an active job is within 24 hours of its
original expiry. Status combines with attention; completed/cancelled plus
attention returns 400. Read scope and MFA apply; no recovery action is performed.

Filtering happens before the page limit. Pages preserve the existing newest
creation-time/UUID ordering and exclusive keyset cursor, with default 50 and
maximum 200 rows. They can inspect matching jobs beyond the diagnostic scan cap.
An attention response has page metadata `coverage: live_matching_retry_jobs_page`,
`filter`, `observed_at`, `has_more` and warning durations. `has_more` agrees with
`next_after` and means another matching row was observed after this page. No total
count or completeness claim for the whole backlog is supplied.

Each listed job retains its original `created_at` guard and now includes optional
`last_progress_at`. Attention requests also include per-job `attention` metadata:
remaining cases, inactivity and known/unknown progress ages, warning flags and
retention time. This uses the same evaluator as consumer health. Older evaluated
jobs keep unknown progress; controls are not mistaken for progress. Summary and
attention data contain no case selections/results, payloads or command receipts.
Review a job and use its existing guarded controls deliberately; the listing does
not reserve it or extend retention.

Pages are live observations. Jobs can progress, pause/resume, expire or enter a
warning between requests. Keep status and attention filters unchanged across
pages. New or newly matching jobs above the cursor require restarting from the
first page; an empty final page is not proof of future health. The PostgreSQL read
keeps target ownership and page rows in one read-only snapshot; separate pages do
not share that snapshot.

Go accepts an optional `EventExecutionLineageRetryJobAttentionOptions` argument;
existing calls without it keep ordinary listing behavior. Node and Python expose
the optional attention query parameter and metadata models. CLI/Go reject an
attention response missing compatible page metadata, so older servers cannot
silently return an ordinary unfiltered list. Other clients should check metadata
when talking to mixed API versions. No new route, migration or registry entry is
needed.

## Review and apply controls to a fixed set of jobs

Export one attention page without executing its controls:

```sh
gregale events subscription-lineage-retry-control-plan-export APP SUB --action pause --attention stalled --status pending --limit 50 --file pause-plan.json
# Review the saved job IDs, original timestamps, actions and command UUIDs.
gregale --json events subscription-lineage-retry-control-plan-apply APP SUB --file pause-plan.json --timeout 10m
```

The private version-1 `gregale.event-lineage-control-plan` file stores stable app
and subscription IDs, a plan UUID, source attention/status/observation and cursor
hint, and fixed `actions`. Each action has `job_request_id`,
`expected_created_at`, original `retain_until`, an action and a reusable
`command_id`. Export defaults to attention `any` and one page of 50 (maximum 200);
action is required. Use `--after` to export another deliberate page into a separate
file. Empty selections fail without publishing a file. Plans are bounded to
128 KiB, privately written and atomically published without replacing any existing
file, using the same filesystem requirements as history exports.

Plans are unsigned/editable. Review or trim entries before the first application;
do not change a submitted entry's job/action/creation guard/command UUID afterward.
Source warning filters describe the export observation, not current job state or
permission to execute. The apply command uses this fixed selection without fetching
a new attention list. A manually reviewed plan can contain different valid actions
for distinct jobs. The plan UUID is metadata; per-job command UUIDs provide the
server-side idempotency. Creating a new plan assigns new command IDs and is not a
substitute for repeating the original file after a lost response.

Apply requires the supplied subscription to match the plan and resolves the app
slug to its saved stable app UUID before any POST. Read scope is needed for this
binding check; existing deploy/write scope and MFA are needed for each control.
The parser rejects oversized/nonregular files, unsupported formats, unknown/null
or duplicate/case-aliased fields, invalid identities/timestamps/actions and repeated
jobs or command IDs. IDs and time zones are normalized only in memory. The file is
never rewritten or marked completed.

Each entry invokes the existing guarded pause/resume/cancel endpoint with its
saved command UUID, sequentially and independently. Other entries continue after
an API error. A verified original receipt produces `acknowledged`; a request error
or incompatible/malformed acknowledgment produces `unconfirmed`, without claiming
whether a mutation committed. Shared binding errors or exhaustion of the total
deadline leave entries `not_attempted`. The report includes every entry and counts;
`complete` means all were attempted, not all succeeded. JSON stdout contains the
report; human output lists per-job outcomes. No selections/results or event
payloads are copied into it. All acknowledgments exit 0; any unconfirmed or
not-attempted entry exits 1; Ctrl-C reports partial results and exits 130.

Repeat the same unchanged plan to acknowledge earlier successful commands and
attempt remaining ones. The server returns each command's original receipt with
current job state and does not reapply it after intervening controls. Receipt
retention, the 128-receipt per-job cap, stale creation guards and expiry still
apply. New UUID no-ops consume slots; new pause/resume commands on terminal jobs
fail, while cancellation keeps its terminal acknowledgment semantics. A batch is
not atomic and has no rollback: earlier successful controls remain committed if a
later entry fails. No client execution journal or retention reservation is made.

Both commands accept a positive overall `--timeout` (default `2m`), and each API
read/control also uses the existing control timeout. A large or slow plan may need
a longer total timeout; otherwise repeat its unchanged file for unattempted jobs.
No new API route, SDK operation, schema migration or registry update is needed.

### Preflight a reviewed batch-control plan

Before applying a fixed control plan, inspect its current jobs and command history:

```sh
gregale --json events subscription-lineage-retry-control-plan-preflight APP SUB --file controls.json --timeout 2m
```

This command uses read-only requests and leaves the plan unchanged. It validates
saved app/subscription scope, creation and retention guards, current job metadata,
and the full bounded receipt journal. Per-job outcomes are `ready`, `no_op`,
`already_acknowledged`, `stale_guard`, `expired`, `command_conflict`, `receipt_limit`,
`terminal`, or `unverified`. Failed or malformed reads are unverified and include
an error; other jobs are still inspected. Existing matching receipts take precedence
over terminal state and receipt capacity. A new no-op command consumes a receipt
slot when applied.

`ready` in the report means all entries are ready, no-op or already acknowledged;
`complete` means all entries were assessed, including definite blockers. Exit 0
means advisory readiness, 1 means a blocker or unverified entry, and 130 means
interruption after partial reporting. Requests have per-read and total deadlines.
Read failures and app-scope failures never imply readiness.

The job and receipt reads are separate observations. State, receipt capacity and
retention may change before apply. Preflight does not check write permission/MFA
or reserve anything. Review the report, then apply the same unchanged file; server
guards remain authoritative. No new UUIDs or files are created by preflight.

### Save execution evidence and resume a control plan

```sh
gregale --json events subscription-lineage-retry-control-plan-apply APP SUB --file controls.json --report run-1.jsonl
gregale --json events subscription-lineage-retry-control-plan-apply APP SUB --file controls.json --resume-report run-1.jsonl --report run-2.jsonl
```

Recovery request, plan, history, and report readers require regular files and
refuse a symlink in the final path component. Journal directory syncing checks
the directory identity before and after opening it.

Reports are new private append-only files (0600), capped at 1 MiB. Apply syncs a
command intent before its POST and the verified result afterward. File creation,
directory sync or persistence failures prevent further control requests and exit
unsuccessfully. The stdout summary remains available; if the latest result could
not be saved, resume treats the persisted intent as uncertain.

Resume requires the same normalized plan, including command UUIDs and original
guards. It reads complete records, ignoring only an unterminated final crash line,
and checks server receipts for every recorded entry before sending another control.
Matching receipts are acknowledged without POST; missing receipts allow the same
command only if read-only preflight finds it eligible. Read failures or blockers
stay unconfirmed. Unrecorded entries use their original plan commands. No new
command UUIDs are generated. Each resume uses a new report, preserving prior files;
use the newest report for subsequent resumes.

A plan hash detects mismatches but does not authenticate editable reports. Read
and control requests remain separate observations; server guards and retained
receipts provide idempotency. Reports contain job progress and receipt metadata,
not event payloads, and do not extend retention or provide atomic batch execution.

### Reconcile an execution report without sending controls

```sh
gregale --json events subscription-lineage-retry-control-plan-reconcile APP SUB --file controls.json --source-report run-1.jsonl --report reconciled.jsonl --timeout 2m
```

The command validates the report against the unchanged normalized plan, binds the
explicit target app/subscription, and checks every fixed command using read-only
job and guarded receipt requests. This includes commands absent from an interrupted
report. It never sends pause/resume/cancel requests or allocates replacement UUIDs.
The source and plan stay unchanged; the destination must be new and is a private,
synced execution journal using the existing 1 MiB limit.

Results add `evidence`: `receipt_present` confirms a matching acknowledgment;
`receipt_absent` means the complete observed journal lacked that command;
`command_conflict` means its UUID was used for another action. `expired`,
`stale_guard`, and `unavailable` indicate missing usable evidence. Only verified
matching receipts produce `acknowledged`; all other entries remain `unconfirmed`.
Missing receipts do not prove that a command never ran. Old local acknowledgments
are rechecked rather than trusted, and separate reads can observe changes.

The JSON summary counts acknowledged and unresolved entries. `complete` means all
entries were assessed and persisted; a complete report can still contain definite
unresolved observations. Read/scope failures, interruption, or persistence failure
make it incomplete. Exit 0 means all acknowledgments were verified and saved, 1
means unresolved entries or persistence failure, and 130 means interruption after
partial reporting. Independent read failures allow other jobs to be inspected;
persistence failure stops further inspection and is included in the summary.

Use the fresh journal for another reconciliation or as `--resume-report` when you
choose to resume controls. Resume rechecks server receipts before retrying, with
the same plan commands and guards. Readers predating the optional `evidence` field
must be upgraded to read reconciled journals. Reconciliation does not extend
retention or turn an editable local report into authenticated evidence.
