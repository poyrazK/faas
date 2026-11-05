# Persisted publication retry policies

Application outbox publications can bind a versioned retry policy in the same
transaction as their event and business writes. Recovery uses that binding across
supported workers and restarts, regardless of worker fallback retry settings.
Existing unbound rows retain their prior behavior until explicitly adopted.

## Stage a bound publication

```go
status, err := preview.SaveEventOutboxWithRetryPolicy(ctx, tx,
    accountID, appID, slug,
    preview.AppPublishEventRequest{
        Key: producerKey,
        Type: "order.created",
        Data: eventJSON,
    }, 7 * 24 * time.Hour,
    preview.EventOutboxRetryPolicy{
        InitialBackoff: time.Second,
        MaxBackoff: 5 * time.Minute,
        MaxAttempts: 20,
    })
if err != nil { return err } // Roll back the business transaction on error.
_ = status
// Commit business writes, the outbox publication and its policy together.
```

Version 1 uses the existing [exponential jitter and Retry-After semantics](event-outbox-retry-policy.md).
Policy normalization/defaults are the same as worker retry options. MaxAttempts
zero disables the attempt limit. New bindings start at revision 1. Read current
settings with `GetEventOutboxRetryPolicy`; returned backoffs are integer
milliseconds, with version, revision, maximum attempts and database update time.
`ErrNotFound` means no retained binding, not proof that an event was not accepted.

The original policy digest is immutable. A repeated save using the same normalized
initial policy returns the retained publication and preserves any later guarded
policy changes. A different initial policy returns `ErrEventOutboxConflict`;
staging is not a policy-update API. Existing content and slug comparisons still
apply. Repeated matching saves also work for an already published bound row.

Binding through staging is limited to unresolved, never-claimed rows without a
claim. Already bound rows only validate their original binding. To adopt legacy
work that has already been claimed, use the audited command below. The legacy
`SaveEventOutbox` helper does not bind or remove policies.

## How recovery selects settings

Both `RecoverNextEventOutbox` and `RunEventOutboxWorker` read the binding while
holding the publication row lock, before checking its attempt limit or recording
a new claim. The captured policy governs that claim's attempt limit, jitter,
Retry-After handling, and SDK transport-retry bypass through settlement. Worker
fixed Backoff or RetryPolicy cannot override a bound row. Operational claim,
HTTP and worker timeouts remain worker settings and can still shorten an attempt.

Rows without a binding use the existing worker fallback settings. Those settings
must still pass ordinary option validation. Missing policy storage, unknown policy
versions, or invalid stored metadata fail as sanitized storage errors before an
HTTP send; they never silently fall back. Published rows are not claimed.

Only the policy command mutates a retained binding through the SDK. All supported
paths lock the publication before writing policy state. Commands reject live
claims. An expired claim may be cleared by a guarded policy command; an older
worker cannot subsequently settle with its discarded token. Attempts remain
cumulative, and no policy revision extends the original retry deadline.

## Adopt or revise with an audited command

```go
status, err := preview.GetEventOutboxStatus(ctx, db, accountID, appID, producerKey)
if err != nil { return err }
current, err := preview.GetEventOutboxRetryPolicy(ctx, db, accountID, appID, producerKey)
revision := int64(0) // Zero explicitly expects an unbound row.
if err == nil {
    revision = current.Revision
} else if !errors.Is(err, faas.ErrNotFound) {
    return err
}

result, err := preview.SetEventOutboxRetryPolicy(ctx, db, accountID, appID, producerKey,
    preview.EventOutboxSetRetryPolicyOptions{
        CommandID: commandID, // Persist a fresh nonzero UUID and these parameters.
        ExpectedUpdatedAt: status.UpdatedAt,
        ExpectedAttempts: status.Attempts,
        ExpectedRevision: revision,
        Policy: preview.EventOutboxRetryPolicy{
            InitialBackoff: time.Second,
            MaxBackoff: 5 * time.Minute,
            MaxAttempts: 40,
        },
    })
if err != nil { return err }
// result.Outcome == "policy_updated"; result.RetryPolicy holds the new revision.
// A blocked publication remains blocked. Resume is a separate explicit command.
_ = result
```

The command requires an unresolved, unexpired publication, no live claim, and
matching updated timestamp, attempt count, and policy revision. Missing rows or
stale guards return `ErrEventOutboxConflict`. Adoption creates revision 1;
subsequent commands increment revision, even when writing the same policy values.
Unknown versions are rejected rather than replaced by default settings.

The policy change, publication metadata update, and payload-free command receipt
commit atomically. Recorded action is `set_retry_policy`; outcomes are
`policy_updated` or `rejected`. A command ID is mandatory. Replays return the
original result and error without changing revision again; changed normalized
policy or guards with the same ID conflict. The generic result is exposed as
`EventOutboxControlResult`, an alias of the existing metadata result, with an
optional RetryPolicy field. Existing receipts decode without that field.

A successful command preserves state, producer key, UUID namespace, payload,
next-attempt timestamp, attempts and RetryUntil. It updates the local timestamp
and clears only an expired claim. It never publishes or automatically resumes
blocked work. To grant more attempts to an exhausted row, raise its saved limit,
then explicitly resume using the updated status guard from result.Publication.
Lowering a limit below the existing count causes the next recovery visit to block
without sending HTTP. Expired or already published work cannot be reopened.

Use the existing [control receipt lookup and history](event-outbox-control-history.md)
after a lost response. Commit uncertainty means the change and receipt may both
have committed. Recorded snapshots are not current-state reads, and replay is
available only while the receipt remains retained.

## Upgrade and legacy adoption

Fresh applications install the updated `EventOutboxSchema`. Existing installations
must upgrade application-owned storage before starting this SDK's recovery code:

1. Drain and stop workers using older SDK code that does not read saved policies.
2. In an application migration transaction, execute the latest EventOutboxSchema
   to create missing tables/indexes, then EventOutboxStoredRetryPolicySchema to
   upgrade the last-code and control action/outcome constraints. Merely re-running
   CREATE TABLE IF NOT EXISTS does not replace existing CHECK constraints.
3. Start upgraded workers. Existing rows remain unbound and use fallback settings.
4. Adopt selected unresolved rows using the guarded command with ExpectedRevision
   zero. Stage new bound work with SaveEventOutboxWithRetryPolicy.

The upgrade adds `gregale_event_outbox_retry_policies` and allows policy command
receipts. It adds no columns to the original outbox, performs no bulk policy
backfill, and preserves event identities, counts, due times and original deadlines.
It includes the earlier attempts_exhausted CHECK upgrade. Helpers never execute
DDL automatically. All migration work is application-owned, not a platform
migration or clone registry update.

Older workers ignore bindings and must not run alongside policy-bound recovery.
A missing policy table makes upgraded recovery fail closed even for unbound rows,
because absence of storage cannot safely be interpreted as absence of a binding.
Legacy save and read-only health helpers remain available, but are not substitutes
for the recovery schema upgrade.

Bindings reference their publication with ON DELETE CASCADE. Pruning the parent
removes its current policy; control history remains independently retained.
Do not delete bindings to bypass policy or recycle historical producer keys.
Applications own permissions and retention. This feature does not change remote
receipt retention, ordering, consumer retry/replay behavior, or business-effect
deduplication guarantees.
