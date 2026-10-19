# Adopt recovery budgets for existing acknowledgment intents

Go, Node and Python can attach a finite v2 claim budget to an existing unbound or v1
pending/unconfirmed intent. This changes only its policy/grant fields and
`UpdatedAt`. It preserves the callback, state, action/reason, claim count,
next-attempt time and expired claim metadata. It sends no callback, runs no
business code and grants no recovery claim.

## Install and coordinate

First install or upgrade the application-owned intent table using the
[budget schema order](event-acknowledgment-retry-budgets.md#install-and-roll-out).
Then explicitly install `faas.EventAcknowledgmentIntentBudgetAdoptionSchema`.
It creates `public.gregale_event_ack_intent_budget_adoptions`, an optional
application-owned ledger separate from v1 policy adoption and budget resume.
Existing staging/recovery and those earlier commands do not require this table.
Helpers never execute DDL or prune history; there is no platform migration.

Retain ledger entries through the full command retry horizon, independently of
intent retention. There is no foreign key to intents and no automatic expiry.
Deleting receipts erases command deduplication. The ledger contains owner/key
identities, a request digest, outcome, recording time and a bounded historical
metadata snapshot on success. It stores no event, callback token/result or claim
capability. Restrict its access alongside other application-owned metadata.

All recovery hosts for enabled scopes must understand budgets. Current Go, Node
and Python workers can recover adopted intents within each SDK's supported
generation range. All three SDKs expose this adoption command.
Older binding-aware workers reject v2, and pre-binding workers may ignore limits;
exclude both before enabling budgets.

## Select and save an explicit command

Read an exact intent metadata snapshot using `ListEventAcknowledgmentIntents`.
Select the intended invocation/generation/attempt explicitly, and authorize the
owner scope and proposed total through the application's operator policy.
Durably save the command UUID, owner/key and **all original options** before
issuing the command. Copy pointed-to policy metadata when retaining the command.

```go
options := faas.EventAcknowledgmentIntentAdoptRetryBudgetOptions{
    CommandID: commandID,
    ExpectedUpdatedAt: selected.UpdatedAt,
    ExpectedDeliveryAttempts: selected.DeliveryAttempts,
    Budget: faas.EventAcknowledgmentIntentRetryBudget{
        MaxDeliveryAttempts: 8,
    },
}
if selected.RetryPolicy != nil {
    expected := *selected.RetryPolicy
    options.ExpectedPolicy = &expected
    options.Budget.Policy = faas.EventAcknowledgmentIntentRetryPolicy{
        InitialBackoff: time.Duration(expected.InitialBackoffMS) * time.Millisecond,
        MaxBackoff: time.Duration(expected.MaxBackoffMS) * time.Millisecond,
    }
}
// Authorize and durably save options and this owner/key BEFORE calling.
receipt, err := faas.AdoptEventAcknowledgmentIntentRetryBudget(ctx, db,
    accountID, appID, selected.InvocationID, selected.ReplayGeneration,
    selected.Attempt, options)
```

Nil `ExpectedPolicy` requires an all-null unbound policy. A non-nil expectation
must be complete valid v1 metadata with zero `MaxDeliveryAttempts`. Its timing
must equal the proposed budget timing after normalization. Adoption cannot retime
v1 work or change an existing v2 budget. Unbound work adopts explicitly selected
timing, defaulting to initial 1 second / maximum 5 minutes. Future claims use the
new binding; an already saved due time remains unchanged.

After adoption, omitted-policy staging preserves the v2 binding. Repeating staging
with the old explicit v1 binding conflicts instead of downgrading it. Coordinate
producer replay options before adopting existing work; consumer receipts and
saved callbacks remain independent of that staging conflict.

The proposed `MaxDeliveryAttempts` is a **total**, not additional claims. It must
be 1..2147483647 and strictly greater than `ExpectedDeliveryAttempts`, which
includes crashes and uncertain committed claims. With five consumed claims and
a total of eight, three remain. Counts and jitter progression never reset.
The initial v2 budget and current `DeliveryAttemptLimit` both become this total.
This command never parks work immediately; existing final-claim settlement and
crash-recovery parking apply afterward. Use the separate
[guarded resume command](event-acknowledgment-retry-budgets.md#grant-a-larger-limit-and-resume)
to raise a parked v2 grant.

Expected time must be nonzero, representable in UTC years 1..9999 and exactly at
PostgreSQL microsecond precision. Generations retain full nonnegative int64;
attempts are 1..2147483647. UUIDs normalize to lowercase. Inputs, normalized budget
and expected policy fields are copied/validated before I/O. Invalid input returns
`ErrInvalidEventConsumerRequest` without recording a command outcome.

## Guards and concurrency

A new command serializes by owner/command UUID, then takes the existing invocation
advisory lock followed by the selected row lock. The update requires:

- Exact timestamp, committed claim count and expected unbound/v1 policy.
- Pending/unconfirmed state, with both existing budget/grant columns null.
- No live claim: expiry is null or no later than database time.
- No newer generation/attempt for this invocation, including terminal newer rows.
- A total strictly above consumed claims, preserving v1 timing when bound.

Stale metadata, wrong policy, live claims, terminal/parked/v2 rows or newer attempts
produce a retained conflict. An expired claim is allowed and its token/expiry
metadata remains unchanged; it can no longer settle under the live-claim fence.
An already in-flight remote callback may still finish. Adoption does not cancel
it, alter its inputs or establish remote failure.

The invocation lock coordinates with staging and other commands; the row lock
coordinates with recovery. A racing recovery claim can make timestamp/count/
liveness guards fail. Configure bounded acquisition/statement/context timeouts;
helpers add no background worker or automatic retry.

## Receipts and uncertainty

| Outcome | Go error | Retained snapshot |
| --- | --- | --- |
| `adopted` | nil | Historical v2 metadata at adoption. |
| `conflict` | `ErrEventAcknowledgmentIntentConflict` | None. |
| `not_found` | `ErrNotFound` | None. |

Mutation/rejection and receipt commit atomically. Other storage failures return no
receipt; a failed ledger insert rolls back adoption. Unknown COMMIT returns no
receipt and preserves `ErrEventConsumerCommitUnknown`. Retry the **exact original
command and inputs** on a fresh connection/context; do not refresh its fences.
Driver diagnostics are sanitized and do not enter returned errors.

Matching retained commands replay before reading the current intent and finish
without another COMMIT. Historical success/rejection survives later recovery,
resume, supersession or intent pruning. A successful replay reports the original
grant, not the current resumed grant. It provides no current ownership or remote
confirmation. Different valid input under a retained owner/command UUID returns
conflict without a receipt or overwrite. A new operator decision needs a new UUID
and newly selected metadata. V1 adoption and resume receipts remain independent.

## Shared request identity

The request digest is SHA-256 over UTF-8
`gregale-event-ack-intent-budget-adoption-v1\n` followed by compact JSON in this
field order:

`version`, `action`, `invocation_id`, `replay_generation`, `attempt`,
`expected_updated_at`, `expected_delivery_attempts`, `expected_policy_version`,
`expected_initial_backoff_ms`, `expected_max_backoff_ms`, `policy_version`,
`initial_backoff_ms`, `max_backoff_ms`, `max_delivery_attempts`.

Version is 1, action is `adopt_retry_budget`, target policy version is 2. Unbound
expectations use zero for all three expected-policy fields; v1 expectations use
version 1 and exact timing milliseconds. Owner/command UUIDs form the ledger key.
Time is exact UTC RFC3339 with fractional trailing zeros trimmed. These fixed
fields define later SDK parity without storing callback material in receipts.

The [buildable Go example](../sdk/go/examples/event-acknowledgment-recovery/budget_adoption.go)
separates command selection/persistence from execution. Local validation covers
SDK/example build and vet, SQLC static compilation, source/DDL contract inspection,
schema parity and documentation checks. No tests/assertions are added or modified,
and no local tests or SDK helper smoke calls run. Hosted behavioral qualification
remains pending for mixed SDK recovery, exact receipt replay, concurrent staging/
claim/adoption, expired claims, cancellation, unknown commits and supersession.

## Node selection, adoption and replay

Node packages the exact Go ledger DDL as
`eventAcknowledgmentIntentBudgetAdoptionSchema`. Install it once beside the
budget-upgraded intent table before commands. No other helper starts requiring
this separate ledger. The command uses the same owner/command key, lock namespaces,
request field order/domain and snake-case historical snapshot as Go.

Use `getEventAcknowledgmentIntentBudgetAdoptionCandidate` to read exact timestamp
fences. It selects one key without a row lock, recovery capability or ledger write.
Ordinary list/staging timestamps retain their diagnostic millisecond format;
never pass those or a `Date` conversion as an adoption fence. Candidate metadata
preserves int64 generation text, while mutation rejects generations above
`Number.MAX_SAFE_INTEGER` with the existing unsupported-generation error before
I/O or numeric conversion. Go/Node receipt interoperability uses the safe range.

```js
const selected = await getEventAcknowledgmentIntentBudgetAdoptionCandidate(pool,
  accountId, appId, invocationId, replayGeneration, attempt, { signal });
const prior = selected.retryPolicy;
if (prior !== undefined && prior.version !== 1) throw new Error('select unbound/v1 work');
const options = {
  commandId, expectedUpdatedAt: selected.updatedAt,
  expectedDeliveryAttempts: selected.deliveryAttempts,
  expectedPolicy: prior,
  retryBudget: { maxDeliveryAttempts: 8,
    retryPolicy: prior === undefined ? undefined : {
      initialBackoffMs: prior.initialBackoffMs, maxBackoffMs: prior.maxBackoffMs,
    } },
};
// Authorize and durably save the complete options and owner/key BEFORE this call.
const receipt = await adoptEventAcknowledgmentIntentRetryBudget(pool,
  accountId, appId, invocationId, replayGeneration, attempt, { ...options, signal });
```

`expectedPolicy` omitted/null requires unbound. Otherwise it must contain complete
own-data v1 fields `version: 1`, `initialBackoffMs` and `maxBackoffMs`, with
`maxDeliveryAttempts` omitted or zero. No timing defaults are applied to the
expectation. Proposed `retryBudget` is required and follows staging bounds/defaults;
v1 timing must match after normalization. The initial total must exceed consumed
claims. All option and nested policy/budget values are copied before asynchronous
work; unknown/symbol keys, accessors, partial expectations, v2 expectations,
invalid numeric bounds and retiming fail before I/O. No fallback or count reset
is introduced by the command.

New commands take the command lock, then the shared invocation/row locks, and apply
all guards described above. Both success and rejection are retained atomically.
The result and nested metadata/policy objects are frozen. An adopted snapshot can
have zero consumed claims and an expired claim timestamp, unlike a resume snapshot.
Only policy/grant fields and `updatedAt` change. V1 adoption and resume continue
to use their own ledgers and validators.

Known committed/replayed `conflict` or `not_found` outcomes throw
`EventAcknowledgmentIntentBudgetAdoptionError` with `.receipt`. Changed valid input
under a retained ID conflicts without a receipt or overwrite. Matching commands
read historical evidence before current rows and finish with ROLLBACK, without
COMMIT. Retrying after later recovery/resume/pruning still returns the original
snapshot. Unknown COMMIT retains `EventConsumerCommitUnknownError`, returns no
receipt and discards the connection; retry the exact original command. Driver
exceptions are sanitized, and joined cleanup cannot erase primary uncertainty.
Abort is cooperative; configure bounded acquisition/query/cleanup timeouts and
join operations before closing pools. No HTTP, business callback, platform renewal
or ingestion-job acknowledgment occurs.

The [Node example](../examples/event-acknowledgment-node/budget-adoption.mjs)
separates selection, durable persistence and execution. Local evidence is generation,
TypeScript build/declarations, SQLC static compilation, shared source/schema checks
and package inspection. No local tests or SDK helper smoke calls run. Hosted
behavioral qualification remains pending for Go/Node historical receipt replay,
concurrent adoption/claims/staging, cancellation, unknown commits and cleanup.

## Python selection, adoption and replay

Python packages the exact shared DDL as
`event_acknowledgment_intent_budget_adoption_schema`, including wheel and sdist.
Install it once beside the budget-upgraded intent table before commands. Existing
staging, recovery, v1 adoption and resume do not require this ledger. No helper
runs DDL or prunes history.

`aget_event_acknowledgment_intent_budget_adoption_candidate` reads one exact key
without a claim, row lock or ledger write. Python lists and this candidate retain
microsecond timestamps and full int64 generations. Select active unbound/v1 work,
authorize the owner/key and total, then durably save the original command before
execution.

```python
selected = await aget_event_acknowledgment_intent_budget_adoption_candidate(
    connection, account_id, app_id, invocation_id, replay_generation, attempt,
    stop_event=shutdown,
)
prior = selected.retry_policy
if prior is not None and prior.version != 1:
    raise ValueError("select unbound/v1 work")
command = EventAcknowledgmentIntentAdoptRetryBudgetOptions(
    command_id=command_id,
    expected_updated_at=selected.updated_at,
    expected_delivery_attempts=selected.delivery_attempts,
    retry_budget=EventAcknowledgmentIntentRetryBudget(
        8, None if prior is None else EventAcknowledgmentIntentRetryPolicy(
            prior.initial_backoff_ms, prior.max_backoff_ms,
        ),
    ),
    expected_policy=prior,
)
# Authorize and durably save command and the owner/key BEFORE calling.
receipt = await aadopt_event_acknowledgment_intent_retry_budget(
    connection, account_id, app_id, invocation_id, replay_generation, attempt,
    command, stop_event=shutdown,
)
```

Options and receipts are frozen, slotted dataclasses. Options must have the exact
`EventAcknowledgmentIntentAdoptRetryBudgetOptions` type. Required `retry_budget`
uses the existing exact budget/policy types and bounds. `expected_policy=None`
requires an unbound row; otherwise it must be the exact stored-policy dataclass
with native integer v1 version/timing and `max_delivery_attempts` None or native
integer zero. No defaults fill partial expectations. Proposed timing must preserve
v1 timing, and the total must exceed consumed claims. Booleans, numeric subclasses,
policy/budget subclasses, v2 expectations and retiming fail before I/O. All
normalized values are copied into a detached request before asynchronous work.

Native integers preserve generation 0..9223372036854775807 throughout framing,
SQL and receipt decoding. Expected timestamp uses explicit-zone RFC3339 with at
most six fractional digits and exact UTC normalization, without floating point.
Go/Python interoperate across the full int64 range; commands involving Node use
its safe range. All three share the request digest, command/invocation locks,
SQL guards and historical snapshot described above. Adopted snapshots may include
zero consumed claims and an expired claim, unlike resume snapshots.

Use an exclusively leased idle autocommit async psycopg connection. Commands and
read-only candidate/replay operations use the existing owned/shielded transaction
protocol. Native cancellation and `stop_event` stop at checkpoints and join active
SQL/cursor/rollback/close operations. Cursor close precedes COMMIT; no cancellation
checkpoint after confirmed COMMIT/ROLLBACK can discard known evidence. Configure
bounded acquisition, statement and cleanup calls, and join before releasing the
connection or closing its event loop.

Success returns a frozen historical receipt. Known committed/replayed conflicts
or missing rows raise `EventAcknowledgmentIntentBudgetAdoptionError` with `.receipt`.
Different valid inputs under a retained UUID conflict without a receipt/overwrite.
Exact retries read historical outcomes before current rows and finish with
ROLLBACK, without COMMIT. Unknown COMMIT preserves `EventConsumerCommitUnknownError`,
returns no receipt and closes the connection; discard it through the pool and
retry every original input on a fresh connection. Sanitized storage failures and
joined cleanup retain primary uncertainty. Callback inputs, counts and due times
remain unchanged; v1 adoption and resume keep separate ledgers and validators.

The [Python example](../examples/event-acknowledgment-python/budget_adoption.py)
separates selection, durable command persistence and execution. Local evidence
covers Python 3.11 syntax, Ruff, isolated regeneration excluding tests, wheel/sdist,
SQLC static compilation and shared source/schema comparisons. No local tests or
SDK helper smoke calls run. Hosted behavioral qualification remains pending for
mixed SDK recovery, historical replay, concurrent adoption/claims/staging,
expired/live claims, cancellation, unknown commits and cleanup.
