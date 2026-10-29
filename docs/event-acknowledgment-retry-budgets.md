# Bounded acknowledgment callback recovery

Go, Node and Python can bind an optional total recovery-claim budget to a new saved callback. An
exhausted intent becomes `parked` and retains its original callback for an explicit
operator decision. A guarded command grants a larger total limit and resumes it.
Unbound and version-1 policies retain unlimited recovery.

## Install and roll out

Fresh applications explicitly install `EventConsumerReceiptSchema` and the current
`EventAcknowledgmentIntentSchema`. The intent schema includes the version-2 budget
columns and expanded state/protected-payload constraints. Existing v1-policy tables
apply `EventAcknowledgmentIntentRetryBudgetSchema` once in an application migration
transaction **before upgrading current Go, Node or Python intent helpers**. Pre-policy tables first
apply `EventAcknowledgmentIntentStoredRetryPolicySchema`. Track these one-time
upgrades in application migration history; CREATE TABLE IF NOT EXISTS is not an
upgrade. The budget upgrade starts with a bounded local lock timeout.

The upgrade adds nullable `retry_max_delivery_attempts` and `delivery_attempt_limit`
columns, extends the existing named constraints, and leaves all existing rows
unbound or v1. No budget, parking state, count reset, callback rewrite or command is
backfilled. Current Go, Node and Python staging/listing/recovery selects these columns even for v1
and unbound rows; missing columns produce sanitized storage errors.

Before resuming, explicitly install `EventAcknowledgmentIntentRetryResumeSchema`.
It creates the optional `public.gregale_event_ack_intent_retry_resumes` command table
for both fresh and upgraded installations. Recovery/staging does not require that
table. Helpers never install DDL or prune intent/command history. These are
application-owned schemas; no Gregale platform migration or registry is changed.

Shared fresh DDL is packaged identically in Go, Node and Python. Current helpers
in all three SDKs understand v2 budgets and parked work. Keep staging,
recovery and diagnostics for enabled scopes exclusively on budget-aware hosts
of any supported SDK. Binding-aware older workers reject unknown v2 metadata;
pre-binding workers can ignore budgets and must also be excluded. The database
constraints validate storage, not worker compatibility. Install the schema, upgrade
all hosts for the scope, then enable budgeted staging. Existing shared v1 behavior
continues across SDKs.

## Bind the initial budget

```go
budget := preview.EventAcknowledgmentIntentRetryBudget{
    Policy: preview.EventAcknowledgmentIntentRetryPolicy{
        InitialBackoff: time.Second,
        MaxBackoff: 5 * time.Minute,
    },
    MaxDeliveryAttempts: 8,
}
queued, err := worker.RunDurableWithRetryBudget(ctx, db, budget, applyBusinessSQL)
```

Policy defaults and whole-millisecond bounds follow the
[adaptive retry contract](event-acknowledgment-go-retry-policy.md).
`MaxDeliveryAttempts` is a required total of 1..2147483647 recovery claims. It is
copied/validated before I/O. The initial v2 binding, intent, business writes and
receipt commit atomically. The callback is sent later by existing recovery.

Other entry points are `SaveCompletionIntentWithRetryBudget`,
`RunDurableWithProcessingLeaseAndRetryBudget`, `SaveRetryIntentWithRetryBudget` and
`SaveDeadLetterIntentWithRetryBudget`. Their receipt replay, processing fence and
known-rollback/exclusive-job preconditions retain the existing
[staging contract](event-acknowledgment-intents.md).

The stored policy is version 2 and retains its immutable initial
`MaxDeliveryAttempts` (`max_delivery_attempts` in metadata JSON). Status separately
exposes `DeliveryAttemptLimit` (`delivery_attempt_limit`), the current total grant.
Both start at the same value. Resume raises only the current grant. Repeating
staging with the original initial binding remains idempotent after resume and
does not reset the grant, count or schedule. A different initial binding conflicts;
omitting a binding preserves it. Existing unbound/v1 rows cannot be converted by
these staging methods or the v1 adoption command. All three SDKs offer a separate
[guarded budget-adoption command](event-acknowledgment-budget-adoption.md) for
existing unbound/v1 work; it preserves callbacks, counts and due times.

## Exhaustion and durable parking

Every committed recovery claim consumes one count, including uncertain claim
COMMIT, crashes, failed preflight and claims that send no HTTP. Callback retries
within one claim retain their existing limits and consume no additional recovery
claims. The total budget therefore bounds claims rather than physical sends.

Claim selection validates the stored policy and current grant under its row lock.
It never increments an exhausted row. If the last allowed claim settles with an
unconfirmed callback, the same fenced settlement writes `state='parked'` and
`last_code='attempts_exhausted'`, and clears the local claim. Confirmation and
supersession retain precedence over exhaustion. Callback tokens/results, original
action/reason/digests, count, policy and previous due time remain saved.

After a crashed or uncertain last claim, the row may remain pending/unconfirmed
until the lease expires and it is due. The next claim probe parks it atomically
without incrementing the count or returning a capability. Direct
`ClaimEventAcknowledgmentIntent` returns `*EventAcknowledgmentIntentParkedError`
with the known committed metadata in `Intent`; `errors.Is` recognizes
`ErrEventAcknowledgmentIntentParked`. An uncertain parking COMMIT returns only the
existing unknown-commit error, with no usable claim or confirmed parking metadata.

Single-iteration recovery returns parked metadata and the parked sentinel.
`claim.Deliver` also retains the callback error when parking commits after an
unconfirmed response. Failed settlement returns no confirmed parking status;
remote effects or local settlement may remain uncertain. Active claim tokens and
database expiry still fence every settlement. An expired in-flight request can
finish remotely; parking does not cancel or change that original callback.

Parked rows are excluded from claim selection. A newer generation/attempt staged
by updated Go, Node or Python supersedes parked predecessors and clears their protected data.
Parking chooses no new business disposition, sends no callback and performs no
renewal, job acknowledgment or business work. A parked completion can coexist with
a committed business receipt; existing receipt/platform fences govern later
redelivery and callback reconciliation.

Retain parked capabilities as unfinished work. Terminal-only retention selects
`confirmed` and `superseded` explicitly. Deleting parked rows loses their saved
callbacks and the local supersession fence; clearing a live application's data is
an application-owned decision.

## Grant a larger limit and resume

Inspect parked metadata, authorize the owner scope and durably retain a new command
UUID and all original inputs before calling:

```go
receipt, err := preview.ResumeEventAcknowledgmentIntentRetryBudget(ctx, db,
    accountID, appID, selected.InvocationID,
    selected.ReplayGeneration, selected.Attempt,
    preview.EventAcknowledgmentIntentResumeRetryBudgetOptions{
        CommandID: commandID,
        ExpectedUpdatedAt: selected.UpdatedAt,
        ExpectedDeliveryAttempts: selected.DeliveryAttempts,
        ExpectedDeliveryAttemptLimit: selected.DeliveryAttemptLimit,
        NewDeliveryAttemptLimit: 16,
    })
```

Expected timestamp is the exact nonzero PostgreSQL microsecond value from listing.
The command requires matching timestamp, count and current limit, a parked v2 row,
no local claim, and no newer generation/attempt for the invocation. The new total
must exceed both the current grant and consumed count and stay at most 2147483647.
Counts never reset; after saturation no larger supported grant is available.

The short transaction takes the command lock, then staging's invocation lock and
the row lock. It changes state/code to `unconfirmed`, raises the current limit and
strictly advances the timestamp. It preserves the initial policy, original
callback, count and due time. It sends no HTTP or business work. The host schedules
the existing recovery loop; the next claim uses the accumulated count for jitter.

| Receipt outcome | Error | Meaning |
| --- | --- | --- |
| `resumed` | nil | Larger grant and resume committed locally. |
| `conflict` | `ErrEventAcknowledgmentIntentConflict` | A guard failed; the rejection is retained. |
| `not_found` | `ErrNotFound` | Exact intent key was absent; the rejection is retained. |

All outcomes commit with their receipt; failed insertion rolls back the mutation.
Successful receipts contain a payload-free historical `Intent` snapshot.
Rejections have none. An exact retry returns the original outcome before checking
current row state, including after settlement, supersession or intent pruning.
A changed request under the same owner/command ID conflicts without overwriting
the original receipt. Reconsidering a rejected command requires a new UUID and
fresh metadata.

`ErrEventConsumerCommitUnknown` returns no receipt. Resolve it with the exact
original command, including old expected timestamp/count/limit and proposed grant,
on recovery. Matching replay reads roll back without another COMMIT. Use bounded
contexts and driver timeouts. Driver diagnostics are sanitized. Receipts have no
foreign key to intents and no automatic pruning; retain them through every possible
command retry. Removing them erases deduplication. Initial staging/adoption receipts
remain independent.

Resume request identity is SHA-256 of UTF-8
`gregale-event-ack-intent-retry-resume-v1\n` followed by compact JSON in this order:
`version`, `action`, `invocation_id`, `replay_generation`, `attempt`,
`expected_updated_at`, `expected_delivery_attempts`,
`expected_delivery_attempt_limit`, `new_delivery_attempt_limit`. Version is 1,
action is `resume_retry_budget`, UUIDs are lowercase and the timestamp is UTC
RFC3339 with fractional trailing zeros omitted. The owner/command UUID is the
receipt key. This framing is shared by all three SDKs. Go/Python preserve full int64
generations; Node mutation remains limited to its safe-integer range.

## Diagnostics and qualification

Listing includes parked state, exhaustion code, immutable initial budget and the
current grant without callback data. Bounded Go, Node and Python health includes parked rows in
`Outstanding` and reports `Parked`, excluding them from due, scheduled, leased,
expired-claim and stalled-eligible counts. Sampling can be incomplete; inspect
coverage before interpreting counts. Go/Node/Python ordinary and checkpointed watches
support opt-in `ParkedThreshold` / `parkedThreshold` / `parked_threshold` alerts; see
[Go](event-acknowledgment-go-checkpoints.md#parked-intent-alerts),
[Node](event-acknowledgment-node-checkpoints.md#parked-intent-alerts) and
[Python evidence/compatibility rules](event-acknowledgment-python-checkpoints.md#parked-intent-alerts).

Recovery passes add a scalar `Parked` counter for known parking mutations and keep
admitting within the existing pass budget. Already parked rows are not counted
again by probes. Empty does not prove global drain or callback confirmation and
can coexist with operator-blocked parked work. Unknown commit and joined shutdown
retain their existing precedence. Per-pass MaxAttempts remains an admission bound.

The [buildable Go example](../sdk/go/examples/event-acknowledgment-recovery/retry_budget.go)
shows atomic staging and explicit resume from saved metadata. Local verification
uses builds, generation and static checks. No local tests or SDK helper smoke runs
are performed. Hosted behavioral qualification remains pending for exhaustion,
crash/unknown-COMMIT recovery, final-claim settlement, resume replay/conflicts,
concurrency, supersession and cancellation across all three SDKs.

## Node budgets and guarded resume

Node exports `eventAcknowledgmentIntentRetryBudgetSchema` (the exact Go upgrade)
and `eventAcknowledgmentIntentRetryResumeSchema` (the shared command ledger).
Existing tables need the budget upgrade even when only unbound/v1 work is used;
pre-policy tables need the v1 upgrade first. Install the optional resume ledger
before resume commands. Schema installation is always an explicit application
migration. Fresh intent DDL already includes the budget columns and constraints.

All six durable staging paths accept a copied `retryBudget` option:
`runDurable`, `runDurableWithProcessingLease`, `runPreparedDurable`,
`saveCompletionIntent`, `saveRetryIntent` and `saveDeadLetterIntent`.

```js
const queued = await worker.runDurable(pool, applyBusinessSQL, { signal,
  retryBudget: { maxDeliveryAttempts: 8,
    retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 } } });
```

`maxDeliveryAttempts` is required, integer 1..2147483647. Nested timing is optional
and defaults to 1000/300000 ms. Top-level `retryPolicy` and `retryBudget` are mutually
exclusive. Values must be own data properties with the documented keys; accessors,
symbol/unknown budget keys, invalid bounds and ambiguous options fail before I/O.
Omitting both keeps unbound staging, and the existing `retryPolicy` option binds
unlimited v1 timing. Worker fallback timing has no new budget field.

Frozen status exposes v2 `retryPolicy.maxDeliveryAttempts` and the separate
`deliveryAttemptLimit`. Original staging remains idempotent after resume; explicit
v1 staging cannot downgrade v2. Newer staging supersedes parked rows and clears
their secrets. Ordinary list/staging timestamps remain diagnostic millisecond
strings. Use `getEventAcknowledgmentIntentRetryResumeCandidate` for the exact
microsecond fences required by resume; never round these through `Date`.

```js
const selected = await getEventAcknowledgmentIntentRetryResumeCandidate(pool,
  accountId, appId, invocationId, replayGeneration, attempt, { signal });
// Authorize the decision and durably save this command and owner scope first.
const command = { commandId, expectedUpdatedAt: selected.updatedAt,
  expectedDeliveryAttempts: selected.deliveryAttempts,
  expectedDeliveryAttemptLimit: selected.deliveryAttemptLimit,
  newDeliveryAttemptLimit: 16 };
// Execute only after saving command; exact retries reuse every original field.
const receipt = await resumeEventAcknowledgmentIntentRetryBudget(pool,
  accountId, appId, invocationId, replayGeneration, attempt, { ...command, signal });
```

The proposed limit must exceed both consumed claims and the expected current
limit, with the same upper bound. Timestamp input has an explicit RFC3339 zone
and at most six fractional digits, normalized to exact UTC without rounding.
Diagnostic selection retains full int64 generations as decimal strings; resume
rejects generations above `Number.MAX_SAFE_INTEGER` before I/O. The request framing,
command/invocation advisory locks, guarded update and retained snake-case receipt
snapshot match Go within this range. V1 policy adoption remains a separate v1-only
operation and cannot attach a budget to existing work.

Known `conflict`/`not_found` commands throw `EventAcknowledgmentIntentRetryResumeError`
with a frozen `.receipt`. Changed inputs under a retained command ID throw conflict
without a receipt. Exact retries read historical outcomes before checking the
current row and roll back the read transaction without COMMIT. Unknown COMMIT
retains `EventConsumerCommitUnknownError`, returns no receipt, and discards the
connection. Synchronous/async driver failures are sanitized; cleanup cannot erase
primary commit uncertainty. Configure bounded pool/query/cleanup timeouts and join
operations before closing the pool.

Direct `claimEventAcknowledgmentIntent` rejects with
`EventAcknowledgmentIntentParkedError` and frozen `.intent` only after parking is
known committed, returning no claim. `recoverNextEventAcknowledgmentIntent` and
`claim.deliver` instead return the known `parked` status, matching Node's existing
status-based uncertain-callback reporting. Final confirmation or supersession
still wins over exhaustion. Failed/unknown settlement returns no parking status.
Bounded passes count `parked` and continue admitting; already parked rows are not
selected. Health adds `parked` to `outstanding` and excludes it from automatic
eligibility metrics. Enable `parkedThreshold` in an ordinary or checkpointed Node
watch for sustained parked-count alerts. See
[notification replay, evidence and checkpoint rollout](event-acknowledgment-node-checkpoints.md#parked-intent-alerts).

The [Node example](../examples/event-acknowledgment-node/retry-budget.mjs) separates
selection, durable command persistence and execution. Build, generation, SQLC
static compilation, package-byte and source-contract checks provide local evidence;
no local tests or SDK helper smoke calls are run. Hosted behavioral qualification
for mixed SDK scopes, concurrency, uncertain commits, cancellation, exhausted
crashed claims and resume replay remains pending.

## Python budgets and guarded resume

Python packages `event_acknowledgment_intent_retry_budget_schema` and
`event_acknowledgment_intent_retry_resume_schema` byte for byte from Go, in both
wheel and sdist. Apply the budget upgrade before current Python intent helpers,
even for v1/unbound work. Pre-policy tables need the v1 upgrade first. Fresh DDL
already has the expanded columns/constraints. The optional resume ledger is
needed only for resume commands. Helpers never install DDL or prune history.

All six durable staging methods accept a separate `retry_budget` keyword:
`run_durable`, `run_durable_with_processing_lease`, `run_prepared_durable`,
`save_completion_intent`, `save_retry_intent` and `save_dead_letter_intent`.

```python
budget = EventAcknowledgmentIntentRetryBudget(
    max_delivery_attempts=8,
    retry_policy=EventAcknowledgmentIntentRetryPolicy(1000, 300000),
)
queued = await worker.run_durable(connection, apply_business_sql,
    retry_budget=budget, stop_event=shutdown)
```

The frozen, slotted budget is copied before asynchronous work. Its total is a
required native integer 1..2147483647; booleans, numeric subclasses and budget/
policy subclasses are rejected. Optional timing defaults to 1000/300000 ms and
retains existing whole-millisecond bounds. Supplying both `retry_policy` and
`retry_budget` fails before I/O. Omitting both retains unbound staging; existing
`retry_policy` staging remains unlimited v1. Fallback timing still has no budget.

Frozen status exposes the immutable initial
`retry_policy.max_delivery_attempts` and separate `delivery_attempt_limit` current
grant. Repeating the original staging binding remains idempotent after resume;
v1 staging cannot downgrade v2. Newer staging supersedes parked predecessors
and clears protected inputs. Diagnostic lists preserve exact microseconds and
native int64 generations. `aget_event_acknowledgment_intent_retry_resume_candidate`
reads one exact key, without a claim/row lock, for an explicit operator decision.

```python
selected = await aget_event_acknowledgment_intent_retry_resume_candidate(
    connection, account_id, app_id, invocation_id, replay_generation, attempt,
    stop_event=shutdown,
)
command = EventAcknowledgmentIntentResumeRetryBudgetOptions(
    command_id=command_id,
    expected_updated_at=selected.updated_at,
    expected_delivery_attempts=selected.delivery_attempts,
    expected_delivery_attempt_limit=selected.delivery_attempt_limit,
    new_delivery_attempt_limit=16,
)
# Authorize the decision and durably save command/key/owner scope BEFORE this call.
receipt = await aresume_event_acknowledgment_intent_retry_budget(
    connection, account_id, app_id, invocation_id, replay_generation, attempt,
    command, stop_event=shutdown,
)
```

Use an exclusively leased idle autocommit async psycopg connection. Resume input
is the exact options dataclass, with native integer count/grant bounds. New total
must exceed the old grant and consumed claims. Expected timestamp is explicit-zone
RFC3339 with at most six fractional digits, canonicalized to UTC without floating
point or precision loss. The guarded SQL also requires parked v2, no claim and no
newer invocation generation/attempt. It preserves the initial binding, count,
action, protected callback and due time; v1 adoption remains a separate operation.

Exact original commands replay retained outcomes before inspecting current rows
and finish the read transaction with ROLLBACK, without COMMIT. Rejections raise
`EventAcknowledgmentIntentRetryResumeError` with a frozen `.receipt` only after
known commit/replay. Changed inputs under a retained ID conflict without a receipt.
An uncertain COMMIT preserves `EventConsumerCommitUnknownError`, returns no receipt
and closes the connection; discard it through the pool and retry unchanged on a
fresh connection. The Go/Python command contract spans full int64 generations;
Go/Node/Python interoperability uses Node's safe range when Node participates.

Public operations use existing shielded ownership: native cancellation or
`stop_event` stops future work at checkpoints, joins active SQL/cursor cleanup and
preserves confirmed command evidence. Cursor close completes before COMMIT, with
no cancellation checkpoint after confirmed COMMIT/ROLLBACK. Cleanup cannot erase
primary commit uncertainty. Bound acquisition/driver/rollback/close calls and
join operations before releasing connections or closing the event loop.

Direct `aclaim_event_acknowledgment_intent` raises
`EventAcknowledgmentIntentParkedError` with frozen `.intent` after known parking
commit, returning no claim. `arecover_next_event_acknowledgment_intent` and
`claim.deliver` return known parked status, as with other Python callback outcomes.
Unknown/failed settlement returns no confirmed parking metadata. Passes count
`parked` and continue admission; already parked rows remain excluded from claims.
Health counts parked work as outstanding while excluding automatic eligibility.
Enable `parked_threshold` in either Python watch for sustained parked-count alerts,
including [checkpoint replay and compatibility](event-acknowledgment-python-checkpoints.md#parked-intent-alerts).
Retain protected parked inputs and monitor the count. The
[Python example](../examples/event-acknowledgment-python/retry_budget.py)
separates staging, metadata selection, durable command persistence and execution.

Local evidence covers Python 3.11 syntax, Ruff, isolated source/schema regeneration,
wheel/sdist inclusion, SQLC static compilation and shared contract comparisons.
No local tests or SDK helper smoke calls are run. Hosted behavioral qualification
for mixed SDK recovery, crashes/unknown commits, concurrent claims/resume,
replay/conflicts, cancellation, cleanup failures and supersession remains pending.
