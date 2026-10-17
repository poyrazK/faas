# Durable outbox publishing pause

Applications can pause new outbox claims for one account/application UUID scope
without stopping workers. The setting lives in the application database and
survives worker restart. It affects SDK outbox recovery only, not direct publish
API calls, other application databases, or Gregale's recipient delivery workers.

## Set and read the scope state

Install the updated EventOutboxSchema on a fresh application database. Existing
installations explicitly apply EventOutboxPublishingControlSchema in their
application migration transaction before upgrading recovery and health code.
Drain and replace older workers first: SDK versions without this check cannot
enforce a pause. Helpers never install DDL or update platform clone registries.

```go
state, err := faas.GetEventOutboxPublishingControl(ctx, db, accountID, appID)
if err != nil { return err }
receipt, err := faas.SetEventOutboxPublishingPaused(ctx, db, accountID, appID,
    faas.EventOutboxPublishingControlOptions{
        CommandID: commandID, // Persist a fresh nonzero UUID and these arguments.
        ExpectedRevision: state.Revision,
        Paused: true,
        Reason: "incident_review",
    })
if err != nil { return err }
_ = receipt.Result
```

A missing scope row means enabled, revision zero, no change timestamp and no
commands. Unavailable storage returns a sanitized storage error instead of
assuming enabled. Reads do not create rows. The setting is authoritative local
metadata, not a platform authorization boundary; use trusted owner configuration
and database permissions.

A successful command increments revision, records the requested paused state and
optional reason, and updates the database timestamp. Setting the current state
again with a new ID still creates a new revision. Reason accepts at most 128
lowercase ASCII letters, digits, underscores, dots and hyphens; it is a reason
code rather than free-form business content. To resume, issue a new command with
Paused false and a freshly read ExpectedRevision. Resume does not reopen blocked
publications or alter their individual policies.

Stale revisions return ErrEventOutboxConflict and record a rejected receipt. The
state revision and last-change timestamp remain unchanged, while the history
sequence advances to include that rejection. Status therefore exposes both
Revision and LastCommandSequence. UpdatedAt remains nil until the first applied
command, even if an initial rejected command has been recorded.

## Claim fence and in-flight work

Each claim transaction takes a shared advisory lock for the owner scope, then
reads its pause setting before selecting a publication. A control command takes
the corresponding exclusive transaction lock. A committed pause therefore fences
new claims: a transaction that observed enabled must commit or roll back before
the pause can commit. Use a parent context deadline to bound command/database
lock waits. The check uses a new Read Committed statement after lock
acquisition, including when waiting on a competing control.

The lock is held only through claim SQL/commit, never HTTP. Claims committed
before the pause can still publish and settle under their existing lease and
request deadlines. Pause is not forced cancellation or proof that external calls
have quiesced. It does not mutate existing tokens, attempt counts, payloads,
producer keys, per-row state, saved policies, due timestamps or RetryUntil.

Direct RecoverNextEventOutbox returns ErrEventOutboxPublishingPaused without
claiming or sending when paused. The worker pool treats paused polls as idle,
uses bounded polling backoff, and reports a local paused observation once per
worker per consecutive paused polling episode. Unpause is noticed on a subsequent
poll. Per-row reconciliation, policy updates, staging, and explicit row resume
remain available while the scope is paused; all subsequent claims still face the
global check.

Deadlines continue to expire during a pause. Recovery does not visit expired rows
while paused, but health counts their expiry. After unpause, normal recovery
blocks expired work without HTTP. There is no pause-duration credit or retention
extension. Missing control storage fails recovery closed before claiming a row.

## Durable commands and history

The scope update, history sequence and typed receipt commit in one database
transaction. Command IDs are case-normalized UUIDs in a separate publishing-control
namespace scoped to account/application. Request identity binds expected revision,
paused state and reason. Repeating the same ID and arguments returns the original
receipt/error without changing state; different arguments conflict and preserve
the first result. Use a fresh ID for a new intervention.

GetEventOutboxPublishingCommand retrieves a retained receipt after a lost response:

```go
receipt, err := faas.GetEventOutboxPublishingCommand(ctx, db, accountID, appID, commandID)
if err != nil { return err }
// Inspect Outcome (paused/resumed/rejected), ErrorCode and Result.
// Lookup success is not command success or a current-state assertion.
_ = receipt
```

ErrEventOutboxCommitUnknown means state and receipt may both have committed. A
same-ID retry serializes behind the first transaction before deciding whether
execution is required. A lookup returning ErrNotFound can miss an in-flight
command and is not proof of failure. After a recorded rejection, correct the
revision and issue a new ID rather than changing parameters under the old one.

ListEventOutboxPublishingCommands returns bounded retained history for the scope:

```go
page, err := faas.ListEventOutboxPublishingCommands(ctx, db, accountID, appID,
    faas.EventOutboxPublishingHistoryOptions{After: afterSequence, Limit: 50})
if err != nil { return err }
// Consume Items and continue with NextAfter when nonzero.
_ = page
```

After is an exclusive nonnegative scope sequence. Limits default to 50 and accept
1–100; one extra row determines NextAfter. Sequence allocation and receipt commit
are serialized by the scope transaction, so later commands cannot commit ahead of
lower sequences. Pagination is a live append view, not a frozen snapshot or a
global ordering guarantee. Pruning can create gaps. These receipts are separate
from per-publication control history and contain no event data, tokens,
credentials or raw server/driver diagnostics.

Keep receipts for the intended replay/investigation horizon. Pruning removes a
command's deduplication evidence; never reuse historical command IDs or assume
safe replay beyond retention. Retain the scope row while that scope, old commands
or pagination positions may be used: deleting it can silently re-enable
publishing, reset revisions and reuse sequence numbers. No helper prunes it.
Current state and history are independent of publication-row pruning. Applications
own permissions, actor attribution, retention, and backup policy.

## Health and alert behavior

Health reads the scope state with its unpublished-row snapshot. PublishingPaused,
PublishingRevision and PublishingUpdatedAt identify an intentional pause.
PausedDue counts otherwise eligible unexpired work without a live lease. Due and
Stalled are zero while paused, while outstanding ages, live leases, scheduled,
blocked and expired counts remain visible.

Due/stall watches treat fresh complete pause observations as unknown or
confirmation_lost with reason publishing_paused, not successful resolution.
Blocked/expired alerts continue because pause does not repair those conditions
or stop deadlines. After resuming, normal sustained fresh proof is required.
No ordering, recipient retry/replay, publication deduplication or consumer-effect
guarantee changes.
