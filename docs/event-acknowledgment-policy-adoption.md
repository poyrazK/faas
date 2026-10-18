# Adopt a retry policy for existing acknowledgment backlog

All three SDKs can explicitly bind a stored retry policy to an existing unbound `pending` or
`unconfirmed` intent. This brings retained callbacks under the same policy contract
as newly bound staging. Adoption changes the three policy columns and advances
`updated_at`; it preserves the action, reason, callback token/result, digests,
delivery count, due time, state and expired claim metadata.

## Install and roll out

First follow the [stored-policy schema and worker rollout](event-acknowledgment-stored-retry-policy.md#install-or-upgrade-first).
Every potential recovery host must understand stored policies before adoption is
enabled. Updated Go, Node and Python recovery does; older workers ignore bindings.
Adoption adds no worker compatibility fence.

Explicitly apply `faas.EventAcknowledgmentIntentPolicyAdoptionSchema` in the
application database. It creates the optional
`public.gregale_event_ack_intent_policy_adoptions` receipt table with a primary key
on account, app and command UUID. This is additional DDL for both fresh and existing
installations. The intent table must already have the policy columns. No helper
installs DDL, and staging/recovery does not require the new receipt table.

Node exports the same SQL as `eventAcknowledgmentIntentPolicyAdoptionSchema`.
Python exports `event_acknowledgment_intent_policy_adoption_schema`. Apply one
shared schema installation, not one per SDK.

Use an application role authorized for these owners. Trusted account/app UUIDs
select the scope; the SDK does not authenticate an operator or provide database
row-level access control. Restrict receipt writes alongside intent writes. Receipt
metadata contains no callback token, result, raw event body or recovery capability.
There is no Gregale platform migration or compliance registry change.

## Select and adopt one intent

Read the exact invocation, replay generation and attempt with
`ListEventAcknowledgmentIntents`. Preserve its `UpdatedAt` and `DeliveryAttempts`.
Generate and durably retain a command UUID and all original inputs before calling:

```go
options := faas.EventAcknowledgmentIntentAdoptRetryPolicyOptions{
    CommandID: commandID,
    ExpectedUpdatedAt: selected.UpdatedAt,
    ExpectedDeliveryAttempts: selected.DeliveryAttempts,
    Policy: faas.EventAcknowledgmentIntentRetryPolicy{
        InitialBackoff: time.Second,
        MaxBackoff: 5 * time.Minute,
    },
}
receipt, err := faas.AdoptEventAcknowledgmentIntentRetryPolicy(ctx, db,
    accountID, appID, selected.InvocationID,
    selected.ReplayGeneration, selected.Attempt, options)
```

Expected time must be nonzero and at PostgreSQL microsecond precision; use the
listed value without rounding or replacing it with local time. The count is
0..2147483647, matching the persisted saturating recovery-claim count. Generation
is nonnegative and attempt is 1..2147483647. Owner, invocation and command IDs are
nonzero UUIDs normalized to lowercase. Policy defaults/bounds are the existing
[Go adaptive settings](event-acknowledgment-go-retry-policy.md).
Validation and policy copying happen before database I/O.

The transaction serializes the owner/command UUID, checks for a retained receipt,
then shares staging's invocation advisory lock and recovery's row lock. Adoption
requires both expected fields, an unresolved state, three null policy columns and
no live database claim. A future due time is allowed and preserved. An expired
claim does not block adoption; its metadata is left intact and it cannot settle
after expiry. An already in-flight callback may still complete; adoption preserves
that callback's identity and the platform's existing idempotency/fencing contract.
Another recovery/staging mutation that wins the row lock causes the
guards to be checked against that committed row. Partial or malformed existing
policy metadata produces a sanitized storage error.

The policy binding, strictly advanced timestamp and receipt commit atomically.
Missing receipt storage or a failed receipt insert rolls back the binding. No
callback is sent, processing is not renewed and no business handler is run.
Adoption does not reset backoff: a future recovery claim continues from the
existing durable count and its captured policy controls unconfirmed settlement.
It does not reschedule the existing due time.

## Resolve outcomes and uncertain commits

Inspect both receipt and error:

| Receipt outcome | Returned error | Meaning |
| --- | --- | --- |
| `adopted` | nil | Binding and historical metadata committed locally. |
| `conflict` | `ErrEventAcknowledgmentIntentConflict` | Bound/terminal/live-claimed row or stale timestamp/count; no adoption. |
| `not_found` | `ErrNotFound` | Exact owner/invocation/generation/attempt was absent. |

All three outcomes are retained. A rejected command stays rejected even if the
claim later expires or the row later appears. After inspection, a new deliberate
attempt requires a new command UUID and newly selected expected values. Already
bound policies conflict even when equal; only a replay of the original successful
command returns its existing success.

Retrying the same owner/command with the same normalized request returns its
historical receipt before examining current intent state. This also works after
settlement, supersession or intent pruning. The successful `Intent` snapshot may
be stale and proves neither current ownership nor remote callback confirmation.
Rejected receipts contain no intent snapshot. Reusing a retained command UUID
with different inputs returns conflict and an empty receipt; it does not overwrite
the original command. Invalid inputs and storage failures return no receipt.

`ErrEventConsumerCommitUnknown` means neither adoption nor rejection is confirmed.
Retry with the same command UUID and **exact original** inputs, including expected
time/count. Do not refresh them while resolving that command. The command lock
waits for an in-flight transaction; a saved receipt resolves a committed result,
while an absent receipt permits the original transaction to be attempted again.
Replay reads do not issue a new COMMIT. Driver error details are sanitized.
Use a bounded host context and database timeouts; no detached operation is started.

The private request digest uses SHA-256 over the UTF-8 prefix
`gregale-event-ack-intent-policy-adoption-v1\n` followed by compact JSON in this
field order: `version`, `action`, `invocation_id`, `replay_generation`, `attempt`,
`expected_updated_at`, `expected_delivery_attempts`, `policy_version`,
`initial_backoff_ms`, `max_backoff_ms`. Version and policy version are 1, action is
`adopt_retry_policy`, IDs are lowercase, time is UTC RFC3339 with fractional trailing
zeros omitted, and delays are normalized whole milliseconds. The owner/command is
the storage key. The digest and JSON framing contain no capability or payload.

Receipts have no foreign key to intents, automatic expiry or pruning. Retain them
through every possible command retry, including old process/job restores. Pruning
a receipt erases deduplication and can allow that command to be evaluated anew.
Receipt retention and payload-free metadata retention are application decisions.

## Node selection, adoption and replay

Use `getEventAcknowledgmentIntentPolicyAdoptionCandidate` to read the exact selected
row without a claim or row lock. It returns frozen `EventAcknowledgmentIntentStatus`
metadata with canonical UTC microsecond timestamps. Ordinary list/staging timestamps
retain their existing millisecond diagnostic format; use the candidate read for the
adoption fence and preserve its `updatedAt` string directly, without `Date` conversion.

```js
import {
  adoptEventAcknowledgmentIntentRetryPolicy,
  getEventAcknowledgmentIntentPolicyAdoptionCandidate,
  EventAcknowledgmentIntentPolicyAdoptionError,
} from '@gregale/sdk-node';

const selected = await getEventAcknowledgmentIntentPolicyAdoptionCandidate(pool,
  accountId, appId, invocationId, replayGeneration, attempt, { signal });
const options = {
  commandId,
  expectedUpdatedAt: selected.updatedAt,
  expectedDeliveryAttempts: selected.deliveryAttempts,
  retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
};
// Durably save options and the selected key under this owner scope before adoption.
try {
  const receipt = await adoptEventAcknowledgmentIntentRetryPolicy(pool,
    accountId, appId, invocationId, replayGeneration, attempt, { ...options, signal });
  // receipt.outcome === 'adopted'; receipt.intent is historical metadata.
} catch (error) {
  if (error instanceof EventAcknowledgmentIntentPolicyAdoptionError && error.receipt) {
    // Retained rejection, including replay: inspect receipt.outcome before a new decision.
  }
  throw error; // The host handles uncertainty/cancellation and preserves original inputs.
}
```

The options type is `EventAcknowledgmentIntentAdoptRetryPolicyOptions`. Its closed
shape accepts `commandId`, `expectedUpdatedAt`, `expectedDeliveryAttempts`, optional
`retryPolicy` and `signal`. Configuration must use own data properties; getters,
symbols and unknown keys are rejected. Policy values are copied before I/O; omitted
policy selects the same v1 defaults as Go. An optional AbortSignal is cooperative
between queries and before COMMIT; it is not part of command identity. Bound active
pool/query operations with driver timeouts and keep the pool available until return.

Expected timestamp input accepts microsecond RFC3339 with an explicit zone and is
canonicalized to UTC for the Go-compatible digest. It cannot be a Date object or
the zero timestamp. Candidate reads preserve full decimal int64 generations;
adoption rejects generations above `Number.MAX_SAFE_INTEGER` before database access
with `event_acknowledgment_intent_unsupported_generation`. It never rounds a
generation. Go commands within this bound and Node commands share the same receipt
and request-digest format, including exact snapshot timestamps and policy metadata.

Successful adoption/replay returns a frozen
`EventAcknowledgmentIntentPolicyAdoptionReceipt`: `commandId`, `invocationId`,
`replayGeneration` (decimal string), `attempt`, `outcome`, `recordedAt` and optional
frozen `intent`. New or replayed rejections throw
`EventAcknowledgmentIntentPolicyAdoptionError` with that receipt and code
`event_acknowledgment_intent_conflict` or `event_acknowledgment_intent_not_found`.
A changed request under the same retained command ID throws conflict without a
receipt. An absent candidate throws not-found without a receipt; it records no
command. Unknown or malformed stored metadata produces a sanitized store error.

An uncertain write COMMIT throws `EventConsumerCommitUnknownError` with the existing
`event_consumer_commit_unknown` code and no receipt. The connection is discarded.
Rollback/release are joined, and cleanup failures cannot replace unknown commit.
Late abort after a confirmed COMMIT preserves its outcome. Storage/cleanup errors
can also occur after effects: retry the exact saved command on a fresh connection.
Matching receipt replay uses ROLLBACK instead of sending another COMMIT, so it
returns retained evidence independently of current intent state.

## Python adoption and replay

Python's existing `alist_event_acknowledgment_intents` returns precise timestamp
strings and native int64 generations. Explicitly select the exact invocation,
generation and attempt from that metadata. Preserve its timestamp string directly:

```python
from faas_sdk import (
    EventAcknowledgmentIntentAdoptRetryPolicyOptions,
    EventAcknowledgmentIntentPolicyAdoptionError,
    EventAcknowledgmentIntentRetryPolicy,
    aadopt_event_acknowledgment_intent_retry_policy,
)

options = EventAcknowledgmentIntentAdoptRetryPolicyOptions(
    command_id=command_id,
    expected_updated_at=selected.updated_at,
    expected_delivery_attempts=selected.delivery_attempts,
    retry_policy=EventAcknowledgmentIntentRetryPolicy(1000, 300000),
)
# Durably save options and the selected key under this owner scope before adoption.
try:
    receipt = await aadopt_event_acknowledgment_intent_retry_policy(
        connection, account_id, app_id, selected.invocation_id,
        selected.replay_generation, selected.attempt, options,
        stop_event=shutdown,
    )
except EventAcknowledgmentIntentPolicyAdoptionError as error:
    # error.receipt is present for known committed/replayed rejection outcomes.
    raise
```

Options must be the exact frozen slotted options type. Native integers exclude
bools/subclasses: generation is 0..9223372036854775807, attempt 1..2147483647, and
expected delivery count 0..2147483647. The timestamp is a nonzero microsecond
RFC3339 string with an explicit zone, normalized to UTC for the shared digest.
The optional policy is the exact existing frozen retry-policy type; None selects
the v1 defaults. All values are copied/validated before database operations.

The returned `EventAcknowledgmentIntentPolicyAdoptionReceipt` is frozen and holds
`command_id`, `invocation_id`, `replay_generation`, `attempt`, `outcome`,
`recorded_at` and optional frozen `intent`. Snapshot timestamps retain microseconds
in canonical UTC form; generation remains an integer in the shared JSON, without
float conversion. Go/Python can resolve the full int64 range. Node adoption retains
its safe-integer bound.

New or replayed rejections raise `EventAcknowledgmentIntentPolicyAdoptionError`
with `.receipt` and code `event_acknowledgment_intent_conflict` or
`event_acknowledgment_intent_not_found`. Changed inputs under a retained command ID
conflict without a receipt. Malformed stored metadata and driver failures use the
sanitized intent-store error. Repeating exact original inputs preserves retained
outcomes independently of current row state.

Supply an exclusively leased idle autocommit async psycopg connection. The helper
owns a short explicit READ COMMITTED transaction and closes its cursor before
COMMIT. It does not enter a caller's transaction. Matching receipt replay finishes
with ROLLBACK and no new COMMIT. The caller owns leasing/returning the connection
and bounds acquisition, driver and statement timeouts.

`stop_event` and native task cancellation are cooperative. The existing shielded
operation joins active SQL, cursor cleanup and rollback before returning. Checks
precede COMMIT; once it starts, late cancellation cannot establish rollback or
erase a confirmed outcome. `EventConsumerCommitUnknownError` returns no receipt
and closes the connection. Cleanup failures cannot replace that exception.
Failed rollback and sanitized store failure also discard the connection. Resolve
uncertain results on a fresh connection using the exact original command; retain
the original expected timestamp/count instead of selecting again.

## Scope and verification

The [buildable Go example](../sdk/go/examples/event-acknowledgment-recovery/adoption.go)
accepts the selected metadata and saved command ID. The
[Node example](../examples/event-acknowledgment-node/policy-adoption.mjs) separates
selection from applying a durably saved command. The
[Python example](../examples/event-acknowledgment-python/policy_adoption.py) follows
the same selection/persistence/application separation. Bound
policy revision/removal, pause/resume, lifetime exhaustion and automatic negative
dispositions are separate increments. Existing staging still conflicts when an
explicit policy is supplied for an existing unbound duplicate.

Builds and static checks do not qualify concurrency or unknown-COMMIT recovery.
Hosted behavioral qualification for adoption/replay, rejected outcomes, live and
expired claims, supersession, rollback, cancellation and response loss is pending.
No local tests or SDK helper smoke checks are run.

## Attach a finite budget to existing work

The v1 command remains a one-time timing binding for unbound work. All three SDKs provide
a separate [guarded budget-adoption command](event-acknowledgment-budget-adoption.md)
for active unbound/v1 intents. It requires an explicit expected policy and exact
timestamp/count fences, rejects live claims/newer attempts, preserves v1 timing,
and attaches a total above consumed claims. Install its separate receipt ledger
before use. V1 command receipts retain their historical behavior and do not become
budget-adoption receipts.
