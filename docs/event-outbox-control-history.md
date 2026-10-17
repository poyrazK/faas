# Durable outbox control history

Applications can opt into retained, payload-free receipts for outbox
reconciliation and resume controls. Supply `CommandID` on the existing Go SDK
options after explicitly installing the updated `faas.EventOutboxSchema`.
Omitting it preserves the previous behavior and writes no history. The SDK never
installs tables or prunes receipts automatically.

## Issue and recover a command

Generate and durably save a fresh nonzero UUID for each logical control before
calling the helper. Keep it with the original parameters when retrying a lost
response. IDs are case-normalized and scoped to account/application across both
control actions and all producer keys, including saved-policy updates.

```go
// Persist commandID and the original status guard before sending the control.
resumed, err := faas.ResumeEventOutbox(ctx, db, accountID, appID, key,
    faas.EventOutboxResumeOptions{
        CommandID: commandID,
        ExpectedUpdatedAt: status.UpdatedAt,
        ExpectedAttempts: status.Attempts,
        AppSlug: currentSlug,
    })
_ = resumed
_ = err

// After an uncertain response, read the retained command result.
receipt, err := faas.GetEventOutboxControlReceipt(ctx, db, accountID, appID, commandID)
if err != nil { return err }
// Retrieval success is not control success: inspect receipt.ErrorCode and Result.
_ = receipt
```

For reconciliation, set `CommandID` on `EventOutboxReconciliationOptions` in
`ReconcileEventOutbox`. The same original request can also be retried through the
control helper: it returns the recorded result and corresponding sentinel error
without executing again. In particular, a resumed row is not required to match
the old status guard again on a retained command replay.

The command identity includes action, producer key, and requested slug. Resume
also includes the expected update instant (normalized to UTC) and attempt count.
Using an existing command ID with different parameters returns
`ErrEventOutboxConflict` and preserves the first receipt. Request timeout is an
execution setting, not command identity. Repeating a command with a new timeout
still returns the original result.

A retained unavailable or failed verification is a recorded observation, not a
request for another remote read. Use a new command ID for a fresh investigation.
A replayed success is the original snapshot, not proof of the row's current state
or current platform retention. Read ordinary outbox status separately for that.

## Atomicity and recorded results

For a command-bearing call, the local state change, result receipt, and per-key
history sequence commit in one application database transaction. If history
storage fails, the state change rolls back. Reconciliation still uses only the
read-only remote verification endpoint; no publication is introduced.

Identical owner/command IDs serialize through a transaction-scoped advisory lock.
The transaction stays open during remote verification, but holds no publication
row or history counter lock across HTTP. RequestTimeout bounds verification, not
the whole call or a competing command's lock wait. Use a parent context deadline
to bound database waits and the complete operation.

Recordable outcomes include successful reconciliation, already-confirmed local
state, unavailable/conflicting/unverified evidence, live-claim rejection, missing
publications, successful resume, and rejected status guards. `ErrorCode` preserves
the local error classification:

| ErrorCode | Control call result |
| --- | --- |
| empty | No control error; inspect Outcome to distinguish confirmation from unavailable evidence |
| `conflict` | `ErrEventOutboxConflict` |
| `unconfirmed` | `ErrEventOutboxUnconfirmed` |
| `busy` | `ErrEventOutboxBusy` |
| `not_found` | `ErrNotFound` |

Malformed inputs, storage failures, and parent cancellation are not retained
command results. A failed commit response returns `ErrEventOutboxCommitUnknown`:
the state change and receipt may both have committed. Reconcile by looking up or
retrying the same command ID and original parameters. A lookup returning
`ErrNotFound` is not proof that an in-flight command failed; a same-ID control
retry waits for the first transaction before deciding whether execution is needed.

Receipts contain action, key, committed per-key sequence, outcome, stable error
code, database recording time, and the original typed local result. They never
store event data, request bodies, capabilities, credentials, or raw driver/server
errors. Keys, app slugs, and owner IDs can still identify business operations;
restrict access and backup retention accordingly. A failed resume may have no
publication snapshot. Reconciliation snapshots can become stale after their read;
only a successful settlement proves its guarded local transition committed.

This is application-owned command history, not an independent authenticated actor
log. Applications can associate command IDs with their own actor records. Legacy
calls without CommandID, publication attempts, and direct SQL interventions are
not included in this history.

## Paginate retained history

```go
page, err := faas.ListEventOutboxControlHistory(ctx, db, accountID, appID, key,
    faas.EventOutboxControlHistoryOptions{After: afterSequence, Limit: 50})
if err != nil { return err }
// Consume page.Items; continue with page.NextAfter if nonzero.
```

History is scoped to one owner and producer key. `After` is an exclusive per-key
sequence, starting at zero. Limits default to 50 and support 1–100; queries fetch
at most one extra row to determine `NextAfter`. Use the same owner/key for all
pages. Sequence numbers are navigation data, not authorization credentials.

A transactional per-key counter allocates sequences at receipt insertion and
holds its row lock until commit. Later receipts for that key cannot become
visible ahead of earlier committed sequences. Counter rollback does not consume
a sequence; pruning can leave gaps. Pagination is a live append view, not a frozen
snapshot or a total count. Recording time is informational: sequence order is the
per-key commit order, not global wall-clock order, business event order, or
publication attempt order.

## Installation and retention

Explicitly re-run the opt-in schema to create
`gregale_event_outbox_controls` and `gregale_event_outbox_control_sequences`.
Command-bearing calls require these tables and fail before changing publication
state if history is unavailable. Existing outbox columns and constraints remain
unchanged. These are application tables, not platform migrations or clone
registry entries.

No foreign key ties receipts to the publication row: retained history remains
readable after local publication pruning. Keep receipts for at least the intended
command replay and investigation horizon. Pruning a receipt removes that command's
result replay/deduplication evidence; never reuse historical command IDs or assume
safe re-execution beyond that horizon. A later same-ID request after pruning can
execute again, subject to the normal publication guards.

Retain the per-key counter while the key or its pagination history can be used,
even when pruning all its receipts. Resetting/deleting counters allows sequence
reuse and invalidates old pagination positions. Counter retention does not preserve
pruned command IDs or results. Applications own both tables' growth, permissions,
retention, and backups. No change is made to producer-key acceptance retention,
retry deadlines, ordering, consumer retries, or business deduplication guarantees.

## Saved policy updates

`SetEventOutboxRetryPolicy` always requires a command ID. It records
`set_retry_policy` with outcome `policy_updated` or `rejected` in this same history.
Its request identity includes the status guard, expected policy revision, and
normalized v1 policy. Successful result metadata includes the resulting policy.
See [saved policy setup and adoption](event-outbox-stored-retry-policy.md) for the
additional schema upgrade. Other controls retain optional CommandID behavior.
