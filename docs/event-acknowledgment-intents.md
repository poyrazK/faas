# Durable Go acknowledgment intents

The opt-in Go intent store persists the callback an application must send after a
crash. `RunDurable` commits its completion intent, consumer receipt and business
writes together. Recovery reads the saved callback and never invokes business code.
Applications still own durable job ingestion, process lifetime and scheduling.
The [Node intent store](event-acknowledgment-node-intents.md) uses the same application-owned schema and protocol.

## Install and protect

Install `faas.EventConsumerReceiptSchema` and
`faas.EventAcknowledgmentIntentSchema` explicitly in the application PostgreSQL
database. Add `EventConsumerProcessingSchema` only when using local preparation
leases. No helper installs, migrates or prunes application tables automatically.
This is application-owned DDL, separate from Gregale's platform schema/registries.

Pending/unconfirmed rows contain the raw acknowledgment token and, for completion,
the saved JSON result. Restrict table and backup access to trusted worker/database
roles and use protected storage. Tokens never appear in diagnostic status structs.
Recovery claims are opaque, redact formatting/JSON and are not a persistence format.
Driver errors from intent operations are sanitized because PostgreSQL diagnostics
can include rejected row contents. Application business errors remain owner-defined.

Construct `EventAcknowledgmentWorker` from authenticated delivery metadata as in
[Go worker acknowledgments](event-acknowledgment-worker.md). Keep exclusive durable
job ownership. Do not mix durable and immediate callback modes for the same attempt.

## Commit completion atomically

```go
queued, err := worker.RunDurable(ctx, db,
    func(workCtx context.Context, tx faas.EventConsumerSQLTransaction) (json.RawMessage, error) {
        _, err := tx.ExecContext(workCtx,
            "UPDATE invoices SET paid = true WHERE id = $1", invoiceID)
        if err != nil {
            return nil, err
        }
        return json.RawMessage(`{"applied":true}`), nil
    })
```

Success means the receipt and `queued.Intent` committed locally. It does not mean
Gregale confirmed delivery. A short pre-commit hook stores the immutable callback
inside the existing receipt transaction, including the receipt replay path. Hook
failure rolls back business writes and any local preparation lease release. The
existing non-durable transaction helpers do not consult this optional table.

The worker first tries a receipt-only replay. If no receipt exists, it confirms the
platform lease and renews while the transaction runs, as in `Run`. Renewal stops
before returning; the recovery host must promptly deliver the pending intent.
Nothing keeps a parked application VM alive. A platform lease that expires before
recovery may require redelivery; the business receipt prevents repeated effects.

`RunDurableWithProcessingLease` adds the existing local preparation fence, without
renewing its row during the final transaction. `SaveCompletionIntent(ctx, db)`
reads an already committed receipt and stages completion without invoking business
code. Use it to reconcile `ErrEventConsumerCommitUnknown`; do not infer rollback
from a failed commit or blindly submit a negative acknowledgment. A successful
retry of durable staging returns the existing intent without changing its payload,
claim, due time or confirmation state. Unknown staging commits return no confirmed
status; repeat the same staging operation after recovery.

## Save an explicit negative decision

After known rollback, stopped work and exclusive job ownership:

```go
intent, err := worker.SaveRetryIntent(ctx, db, "dependency_unavailable")
// Or worker.SaveDeadLetterIntent(ctx, db, "invalid_business_input").
```

The decision commits locally before any callback is sent. Receipt lookup and intent
write use the consumer identity lock, so a discovered committed receipt blocks the
negative decision with `ErrEventConsumerReceiptCommitted`. A mismatched immutable
receipt returns a conflict. During delivery, recovery checks again for a committed
receipt and supersedes a negative intent with code `receipt_committed` rather than
sending it. That absence check is not an exclusive lock against unrelated legacy
workers; stop work and use durable mode consistently.

A valid intent binds owner account/app, invocation ID, replay generation, attempt,
receipt/input digests, app slug, advertised lease, token digest, action and exact
reason/result. Changing these inputs for an existing claim returns
`ErrEventAcknowledgmentIntentConflict`. A newer claim for the same invocation is
ordered by replay generation, then attempt. Staging it supersedes unfinished older
intents and invalidates their local recovery capabilities. Older confirmed rows
remain confirmed history. A stale stage cannot overwrite a newer admitted claim.
A durable business transaction conflicting with an existing negative intent rolls
back; business callbacks must contain only transactional database writes.

## Recover one callback

For a finite concurrent pass with aggregate progress and joined shutdown, use
[`RecoverEventAcknowledgmentIntents`](event-acknowledgment-go-recovery-pass.md).
The single-iteration API below remains available for host-owned scheduling.

Run a bounded iteration from an application-owned durable recovery host:

```go
status, err := faas.RecoverNextEventAcknowledgmentIntent(ctx, db, client,
    accountID, appID, 30*time.Second,
    faas.EventAcknowledgmentWorkerOptions{}, time.Second)
```

`ErrEventAcknowledgmentIntentEmpty` means no row is currently due and unclaimed.
The host decides when to poll again, its concurrency and shutdown policy. Claims
use `FOR UPDATE SKIP LOCKED`, owner scope and database time. The claim duration must
be whole milliseconds from one second through five minutes. Callback options have
the same bounds as the worker helper; the final argument is retry scheduling
backoff, from one millisecond through one hour in whole milliseconds.

`ClaimEventAcknowledgmentIntent` plus `claim.Deliver` exposes the same two steps
when the host needs separate scheduling. The database lease bounds the HTTP context
using remaining database-clock time and a conservative round-trip allowance. Saved
payload/token hashes are checked before returning a claim. Delivery always uses
the original action, token and JSON/reason, with existing server idempotency and
attempt fencing. It does not renew the platform lease or execute business code.

Settlement rechecks the local claim token, state and database expiry. Lost or
superseded recovery claims cannot settle another worker's row. A callback may
already have succeeded when settlement loses its lease or response; the next
worker repeats the same callback safely. There is no transaction across PostgreSQL
and HTTP, and local supersession cannot cancel an already in-flight request. The
platform's token/attempt fence protects a replacement platform claim.

## Diagnostics and retention

`ListEventAcknowledgmentIntents(ctx, db, accountID, appID, invocationID, limit)`
returns 1–100 retained metadata rows (empty invocation ID lists the owner scope).
It exposes no event body, completion result, platform token or recovery token.
Delivery attempts count local recovery claims and saturate at 2,147,483,647.

| State | Meaning |
| --- | --- |
| `pending` | Saved callback is ready or leased for its first recovery attempt. |
| `unconfirmed` | Callback outcome is uncertain; saved inputs remain and next due time uses database time plus backoff. |
| `confirmed` | Platform callback returned a matching successful disposition and local settlement committed. |
| `superseded` | A newer local claim replaced unfinished work, the platform rejected an unavailable claim, or a committed receipt blocked a negative callback. |

Confirmed/superseded rows clear their stored raw platform token, result and recovery
claim. They retain diagnostic digests and metadata; an in-memory claim remains a
secret until discarded. Unconfirmed rows retain protected callback data. Store
failures and canceled calls may leave settlement uncertain: inspect diagnostics or
wait for the old recovery lease to expire. An unknown claim commit returns no usable
claim. A local `confirmed` negative intent confirms the requested retry/dead-letter
callback, not business processing or eventual delivery success.

No automatic retention policy is installed. Do not prune business receipts while
redelivery/replay can repeat effects. Retain intent metadata while stale worker/job
restores can recur; deleting the newest rows erases the local supersession fence.
Terminal payload clearing is not erasure from backups. This feature adds no new
ordering guarantee, exactly-once external effects, notification service or platform
rollout. Failures and recovery remain independent per consumer delivery.

For bounded backlog summaries and sustained local stall alerts, see
[acknowledgment intent recovery health](event-acknowledgment-intent-health.md).
