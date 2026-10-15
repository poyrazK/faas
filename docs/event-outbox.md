# Transactional application event outbox

The Go SDK can stage an application event in the same PostgreSQL transaction as
business writes, then publish it after commit. This closes the failure window
between committing business state and calling Gregale. It uses application-owned
storage and the existing application producer-key endpoint; it does not replace
the platform's internal routing outbox.

## Install and stage

Install `faas.EventOutboxSchema` explicitly in the application's business database.
The SDK accepts `database/sql` transactions and does not import a PostgreSQL driver.
No helper installs, migrates, or prunes tables. Restrict table access and protect
backups: unfinished and blocked rows contain original business event data.

```go
func recordOrder(ctx context.Context, db *sql.DB, accountID, appID, slug, orderID string) error {
    tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
    if err != nil { return err }
    defer tx.Rollback()

    // Perform the application's business writes through tx here.
    data, err := json.Marshal(map[string]string{"order_id": orderID})
    if err != nil { return err }
    _, err = faas.SaveEventOutbox(ctx, tx, accountID, appID, slug,
        faas.AppPublishEventRequest{
            Key: "order-created-" + orderID,
            Type: "order.created",
            Data: data,
        }, 7 * 24 * time.Hour)
    if err != nil { return err }
    return tx.Commit()
}
```

Use a new, stable key for each logical business event, scoped to the canonical
account/application UUIDs. Do not use a key previously published outside this
outbox. Retries of the same business operation must use the original key and
request. The helper sets `expected_app_id` to the saved application UUID. A
nonmatching supplied guard is rejected.

The saved marshaled request and slug are immutable through `SaveEventOutbox`.
Repeated identical saves return the retained row, including terminal state,
without resetting the retry window or reopening publication. Different request
bytes or slug return `ErrEventOutboxConflict`; the local comparison is stricter
than the server's normalized JSON comparison. For example, changing object key
order or an explicit occurrence time may conflict. Preserve explicit times on
transaction retries. Omitting time leaves the first ingress time to Gregale.
The complete saved request must fit the existing 1 MiB publication limit.

Roll back the entire business transaction on any staging error. The helper does
not commit, roll back, or send HTTP. A failed commit response may still mean the
transaction committed: reconcile business state and the retained outbox row
before redoing effects. Use database uniqueness/business idempotency for those
retries; an outbox row alone does not deduplicate arbitrary business writes.

## Recover publication

Run recovery in an application-owned worker, using credentials for the saved
account and the existing publication authorization requirements:

```go
status, err := faas.RecoverNextEventOutbox(ctx, db, client, accountID, appID,
    faas.EventOutboxRecoveryOptions{
        ClaimDuration: 30 * time.Second,
        RequestTimeout: 10 * time.Second,
        Backoff: 5 * time.Second,
    })
// ErrEventOutboxEmpty: nothing due; wait before polling again.
// ErrEventOutboxUnconfirmed: uncertainty was saved; retry when due.
// ErrEventOutboxBlocked: inspect status.LastCode and reconcile before intervention.
// Other errors can leave a lease or a remotely accepted publication outstanding.
_ = status
_ = err
```

Each call claims at most one due row with `FOR UPDATE SKIP LOCKED`, commits the
claim, and sends HTTP outside the transaction. Concurrent workers recover
independent rows. A crashed worker's claim becomes eligible after lease expiry.
The claim token and database-clock expiry fence settlement; a stale worker cannot
overwrite a newer claim. Attempts count recovery claims, not transport requests.
A lost claim or failed settlement can follow successful remote acceptance.
`ErrEventOutboxCommitUnknown` means the claim transaction may have committed;
wait for its lease to expire rather than assuming no claim exists.

Defaults are shown above. Claim duration supports one second–five minutes in
whole milliseconds. Request timeout must be positive (at least one millisecond)
and shorter than the claim duration. Backoff supports one millisecond–one hour in
whole milliseconds and is capped at the original retry deadline. The HTTP context
is additionally bounded by the remaining database lease/deadline, accounting for
time spent reading the lease. Transports must honor context cancellation.

Successful responses must match the saved application, account, derived source,
and derived event ID and contain an acceptance time. Confirmed rows become
`published`, retain the receipt's acceptance timestamp, and clear the payload.
Transport/server failures or malformed success responses become `unconfirmed`.
HTTP 4xx other than 408/429 become `blocked`, including identity/content conflicts
and authorization failures requiring operator action. The SDK returns stable
local errors and codes, without server/driver diagnostic text that might contain
payload data. Cancellation can prevent settlement; lease expiry allows recovery.

`GetEventOutboxStatus` reads one key's retained local metadata and returns
`ErrNotFound` for an absent row. It exposes no payload or claim token; producer
keys can themselves be sensitive business identifiers. A `published` status means
Gregale acknowledged durable acceptance, not that every consumer completed.

## Retention, identity and ordering

A retry window defaults to seven days and accepts one second–seven days, measured
from the database time when the row was first staged, not commit or first send.
It never extends on saves, claims or retries. Expired work becomes `blocked` with
`retry_window_expired` when recovery next visits it, without publishing again.
Blocked work requires explicit operator review. Guarded resume is available
within the original deadline; there is no deadline extension or replacement-key API.

Gregale's producer-key deduplication lasts only while the original acceptance
receipt remains retained. Normal settled receipts become eligible for pruning
after 30 days. The shorter fixed outbox window bounds retries for fresh keys
under that policy; it is not an indefinite exactly-once guarantee. Never extend
the deadline, manually prune acceptance evidence early, recycle a historical key,
or replace an uncertain key simply to bypass a conflict. After the window expires,
use retained publication verification/status and business evidence to decide what
happened. Unavailable evidence is not proof that publication failed.

Use a server containing the `expected_app_id` guard shipped with this feature.
Publication and read-only content verification reject a mismatching resolved app
with 409 before admitting/comparing an event. This prevents a deleted/recreated
slug from silently moving an intent into a new UUID namespace. App rename can
cause 404 and block recovery; use the reconciliation slug override and guarded
resume to correct routing while preserving the original UUID, key, payload and
deadline. Deploying only the SDK
against a server without this contract is unsupported.

Retain local published identities for the application's idempotency horizon;
pruning them permits staging that key again. Retain unresolved/blocked rows until
reconciliation and apply an explicit payload/backup retention policy. No helper
prunes them automatically. This application table is not part of the platform
clone registry or platform database migrations.

Concurrent publication and retries can reorder events, including events from
successive business transactions. There is no global, per-app, or per-consumer
ordering guarantee. Existing recipient retry, replay and dead-letter behavior
remains independent; consumers must deduplicate their own effects. Account/app
arguments scope SQL but are not a database access-control boundary: use trusted
worker configuration and appropriate database permissions.

## Reconcile retained acceptance without publishing

`ReconcileEventOutbox` sends the original saved request to the read-only content
verification endpoint. It never calls publication, resumes work, or extends a
retry deadline. It can settle an unresolved row after its retry deadline if a
matching retained acceptance is still available.

```go
review, err := faas.ReconcileEventOutbox(ctx, db, client, accountID, appID, key,
    faas.EventOutboxReconciliationOptions{
        // Optional after an app rename; the saved UUID guard still applies.
        AppSlug: currentSlug,
        RequestTimeout: 10 * time.Second,
    })
if err != nil { return err }
switch review.Outcome {
case "published", "already_published":
    // Local durable acceptance is confirmed; consumer completion is separate.
case "unavailable":
    // Leave unresolved. Do not infer failure or automatically resume/replace.
}
```

Verification uses the existing apps:read/admin, MFA, and app ownership
requirements; publication-only credentials may lack permission for this read.
The request timeout defaults to ten seconds and accepts one millisecond–30
seconds. HTTP/driver failures return sanitized local errors, with no raw payload
or server diagnostic text. A live recovery lease returns `ErrEventOutboxBusy`.
Already published rows return local metadata without another HTTP request.

A confirmation requires status `match`, the expected application/source/event ID,
the owned account and event ID in the receipt, and a nonzero acceptance instant
within the original `[CreatedAt, RetryUntil]` interval and no later than the
verification observation. Clock skew or evidence outside that interval remains
unverified and requires manual review. A retained key/content match by itself
cannot prove that a later replacement acceptance was the original submission.

Only a valid match can transition the row to `published`, retain its acceptance
time, and clear its payload and expired claim. The local update checks the
original state, updated time, attempt count, request digest, slug, and claim expiry,
and rejects a live claim. A worker claim or control change during verification
therefore prevents settlement. A stale snapshot returns `ErrEventOutboxConflict`;
read the current row before deciding whether to try again.

Outcomes `unavailable` (successful observation with no retained evidence),
`conflict` (`ErrEventOutboxConflict`), and `unverified`
(`ErrEventOutboxUnconfirmed` for verification failure or invalid evidence) leave
local state unchanged. The returned `Publication` is the local snapshot read
before HTTP unless settlement succeeded, so it can already be stale. Published
rows remain locally confirmed even if the platform later prunes their receipt;
`already_published` is not a fresh remote retention assertion.

## Explicit guarded resume

After investigating a rejection and fixing credentials or a renamed slug, an
operator can explicitly re-enable a blocked row:

```go
status, err := faas.GetEventOutboxStatus(ctx, db, accountID, appID, key)
if err != nil { return err }
resumed, err := faas.ResumeEventOutbox(ctx, db, accountID, appID, key,
    faas.EventOutboxResumeOptions{
        ExpectedUpdatedAt: status.UpdatedAt,
        ExpectedAttempts: status.Attempts,
        AppSlug: currentSlug, // Omit to retain the saved slug.
    })
_ = resumed
return err
```

Resume is a local database control and sends no HTTP. It requires a blocked row,
matching nonzero updated timestamp and attempt count, no claim, and a retry
deadline still in the future according to the database clock. The operation sets
state `unconfirmed` and schedules immediate recovery. It preserves UUIDs, producer
key, request bytes/digest, attempt count, and original retry deadline. A supplied
slug changes only the dispatch destination; the embedded expected-app-ID guard
still prevents publication to a replacement app. It does not prove that credentials
or the chosen slug are now correct; a later rejection can block the row again.

Expired, published, missing, nonblocked, or concurrently changed rows return
`ErrEventOutboxConflict` without reopening work. After a lost control response,
read status again: replaying an old guard is not assumed successful. No helper
automatically resumes work because verification was unavailable. This control is
an explicit operator decision within the existing retry policy, not new evidence
that publication failed. Resume cannot alter payloads or resolve content conflicts.

`SaveEventOutbox` still never changes a saved slug. After an explicit correction,
a repeated save must use the corrected slug and identical request to match the
retained row.

## Paginate unresolved work

`ListEventOutbox` returns payload-free blocked and unconfirmed rows, including
`AppSlug`, attempts, claim expiry and retry deadline. `State` can select `blocked`
or `unconfirmed`; empty includes both. Pending and published rows are excluded.

```go
page, err := faas.ListEventOutbox(ctx, db, accountID, appID,
    faas.EventOutboxListOptions{State: "blocked", Limit: 50, After: cursor})
if err != nil { return err }
// Process page.Items; pass page.NextAfter unchanged to fetch the next page.
```

The default limit is 50, supported range 1–100. Queries fetch at most one extra
row to determine whether another page exists. Order is the exact producer key
under PostgreSQL `C` collation, not creation or publication order. Cursors bind
account, application and state filter, and a mismatched cursor is rejected. They
are unsigned local SDK navigation data, not an authorization credential, and can
contain sensitive producer keys. Keep them opaque in application interfaces.

Pages are live views. Concurrent state changes, inserts before the cursor, or
pruning can omit rows from a traversal; periodically start from an empty cursor.
Pagination is not a durable work checkpoint, total count, or historical snapshot.
For an existing outbox, explicitly re-run the opt-in DDL to add
`gregale_event_outbox_unresolved`; existing columns and constraints do not change.

## Recovery health and alerts

Use `GetEventOutboxHealth` for bounded counts and eligibility ages, and
`WatchEventOutboxHealth` for sustained local backlog, stall, blocked and expiry
notifications. See [coverage, freshness and alert semantics](event-outbox-health.md).

## Retained control history

Supply `CommandID` on reconciliation or resume options to record the result
atomically with any local state change and replay it after a lost response.
See [durable control receipts](event-outbox-control-history.md) for schema setup,
parameter binding, lookup, pagination, and retention. Calls without an ID preserve
the earlier behavior and do not write history.

## Deadline-aware retry policies

Set `RetryPolicy` on recovery options for capped exponential jitter, 429/503
Retry-After guidance, and optional lifetime attempt limits. Fixed `Backoff`
remains the default. See [the retry policy guide](event-outbox-retry-policy.md),
including the explicit CHECK-constraint upgrade for existing installations.

## Recovery worker lifecycle

`RunEventOutboxWorker` provides a blocking worker pool with bounded concurrency,
idle polling backoff, per-operation timeouts, local outcome callbacks and graceful
drain. See [worker setup and shutdown semantics](event-outbox-worker.md). The
single-iteration recovery helper remains available for custom supervisors.

## Persisted retry settings

`SaveEventOutboxWithRetryPolicy` binds retry settings atomically with the event.
Recovery uses the saved policy over worker defaults. `SetEventOutboxRetryPolicy`
provides guarded, audited adoption and revision for unresolved work without
resetting attempts or deadlines. See [saved policies and required rollout](event-outbox-stored-retry-policy.md)
before upgrading existing workers.

## Scope publishing pause

Use `SetEventOutboxPublishingPaused` for guarded, durable app-scope pause/resume.
Workers stop claiming while existing claims can finish; keys, schedules and
deadlines remain unchanged. See [publishing controls and required upgrade](event-outbox-publishing-control.md)
for command history and pause-aware health semantics.

## Shared recovery rate limits

Use `SetEventOutboxRateLimit` for a durable app-scoped interval and burst shared by
all upgraded workers using the same database. Claims consume a token atomically
with the lease and attempt; unavailable capacity does not spend attempts or extend
retry deadlines. See [rate-limit configuration, upgrade and health](event-outbox-rate-limit.md).
