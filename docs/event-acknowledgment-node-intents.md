# Durable Node acknowledgment intents

`worker.runDurable` commits a completion intent, the consumer receipt and business
writes in one application PostgreSQL transaction. A separate recovery host sends
only the saved callback. If the process dies after COMMIT but before acknowledgment,
recovery does not invoke business code. The protocol and application-owned table
are shared with the [Go intent store](event-acknowledgment-intents.md).

## Install and protect

Explicitly install the exported `eventConsumerReceiptSchema` and
`eventAcknowledgmentIntentSchema` in the business database. The intent DDL is
packaged from the existing Go schema by `scripts/gen-operation-inbox-schema.py`.
No helper installs, migrates or prunes either table. There is no platform migration
or new API route. Existing immediate worker and receipt calls do not use this table.

Existing installations must apply `eventAcknowledgmentIntentStoredRetryPolicySchema`
once in an application migration transaction before upgrading current Node
intent workers. Durable staging accepts optional `retryPolicy` and recovery
captures saved bindings under the row lock. All-null bindings retain fallback.
See [upgrade, staging APIs and rollout](event-acknowledgment-stored-retry-policy.md).

Current Node helpers also require the budget-schema upgrade, even for v1/unbound
work. Apply `eventAcknowledgmentIntentRetryBudgetSchema` after the v1 upgrade and
before upgrading helpers. Optional `retryBudget` binds a finite total claim limit;
exhaustion parks saved callbacks, and an explicit receipt-backed resume grants more
claims. See [budget staging, migration order and resume](event-acknowledgment-retry-budgets.md#node-budgets-and-guarded-resume).

Pending/unconfirmed/parked rows contain the raw platform token and completion JSON.
Restrict database, backup and worker access and use protected storage. Diagnostic
statuses exclude tokens, results, original events and recovery capabilities.
Claim objects redact string, inspection and JSON formatting and are process-local;
they are not a persistence format. Intent SQL errors omit driver diagnostics,
which may contain rejected rows. Business errors remain application-owned.

Configure `FaaSClient` as in the [worker guide](event-acknowledgment-node-worker.md),
plus bounded pool acquisition, query and PostgreSQL statement timeouts. Authenticate
and authorize ingress before constructing the worker. Persist original delivery
inputs before returning HTTP 202, and retain exclusive durable-job ownership.
Do not mix durable and immediate callback modes for the same attempt.

## Commit completion atomically

```js
const queued = await worker.runDurable(pool, async (tx, signal) => {
  signal.throwIfAborted();
  await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [invoiceId]);
  return '{"applied":true}';
}, { signal: job.signal });
// queued.receiptConfirmed is true; queued.intent is locally committed.
// The intent recovery host now owns callback delivery. The ingestion job can ack.
await job.ack();
```

Only transactional writes through `tx` belong in the callback. External effects
need an outbox or their own idempotency protocol. The internal pre-commit hook also
runs when replaying a receipt; hook failure rolls back the entire transaction.
Intent conflicts therefore roll back new business writes as well.

Without a receipt, the worker confirms the platform lease and renews during the
transaction, using the existing cancellation rules. Renewal stops and joins before
returning. `runDurable` sends no terminal callback. Prompt recovery is necessary:
HTTP 202 and the helper do not keep a VM awake or extend the platform lease after
processing. An expired attempt may need redelivery; the receipt still prevents
repeated committed database effects for that acceptance and consumer.

`worker.saveCompletionIntent(pool, { signal })` reads a committed receipt and
stages its exact completion without invoking business code. Use it after
`EventConsumerCommitUnknownError`. Unknown intent/receipt COMMIT outcomes return no
confirmed intent result, preserve that error type and discard the connection. Do
not infer rollback or automatically choose a negative acknowledgment.

A durable queue can retry `runDurable` with its original inputs and exclusive
ownership: the consumer identity lock waits for an earlier transaction and a saved
receipt skips the handler. If no receipt exists, new work still requires confirmed
platform renewal. Use receipt-only staging when the owner wants reconciliation
without permitting new business work. Missing receipts require an explicit owner
recovery policy.

For preparation outside the transaction, `runDurableWithProcessingLease` composes
the [Node local processing fence](event-consumer-processing-node.md) with atomic
receipt/intent staging. It does not renew a local lease during the locked final
transaction or maintain the platform lease during external preparation.
[`runPreparedDurable`](event-acknowledgment-node-preparation.md) manages both
renewal loops during bounded preparation and joins local renewal before the same
atomic completion. It stages an intent without sending a terminal callback.

## Persist explicit negative decisions

After known rollback, stopped work and exclusive job ownership:

```js
const intent = await worker.saveRetryIntent(pool, 'dependency_unavailable', { signal });
// Or worker.saveDeadLetterIntent(pool, 'invalid_business_input', { signal }).
```

The save sends no HTTP callback. Preserve the selected action/reason with the
owned job so uncertain saves repeat the same decision. A matching receipt blocks
negative staging with `event_consumer_receipt_committed`; a mismatched receipt
conflicts. Recovery checks for a receipt again and marks a negative intent
`superseded` / `receipt_committed` instead of sending it. That absence check is
not an exclusive lock against unrelated legacy workers; stop work and use durable
mode consistently. No exception automatically chooses retry or dead-letter.

Intent identity binds account/app, invocation, replay generation and attempt, plus
consumer/input digests, app slug, advertised lease, capability digest and exact
action/reason/JSON digest. Repeating the same stage returns its existing status
without resetting claims, counters, due time or confirmation. Changed immutable
inputs conflict. Newer generation/attempt pairs supersede unfinished older rows,
clear their secrets and invalidate their recovery claims. Stale stages cannot
replace newer rows. Confirmed history remains confirmed.

## Recover one callback

For finite catch-up with controlled concurrency, use the separate
[bounded recovery pass](event-acknowledgment-node-recovery-pass.md). It composes
this same primitive, joins admitted work and exposes frozen aggregate progress;
the host still decides when another pass runs.

```js
import {
  recoverNextEventAcknowledgmentIntent, EventAcknowledgmentIntentError,
} from '@gregale/sdk-node/preview';

try {
  const status = await recoverNextEventAcknowledgmentIntent(pool, accountId, appId, {
    signal, claimDurationMs: 30000, backoffMs: 1000,
  });
  // Inspect status.state and status.lastCode; resolution alone is not confirmation.
} catch (error) {
  if (!(error instanceof EventAcknowledgmentIntentError)
    || error.code !== 'event_acknowledgment_intent_empty') throw error;
  // No due, unclaimed row. The host chooses when to poll again.
}
```

`claimEventAcknowledgmentIntent(pool, accountId, appId, { signal, claimDurationMs })`
then `claim.deliver(pool, { signal, backoffMs, callbackOptions })` exposes separate
steps. Claims use owner scope, database time and `FOR UPDATE SKIP LOCKED`.
`claimDurationMs` is an integer from 1000 through 300000, default 30000.
`backoffMs` is an integer from 1 through 3600000, default 1000. `callbackOptions`
uses the existing worker's lease-dependent bounds and defaults; `recoverNext`
validates it against the selected row before committing the claim. Separate claim
calls can also supply `callbackOptions` for validation before claiming.

Delivery and `recoverNext` also accept an optional `retryPolicy` with initial
and maximum millisecond caps. The bounded pass accepts the same option. It uses
persisted claim counts for exponential full jitter and retains database-clock
due times. Omit `backoffMs` when enabling it; omitted policies keep fixed-delay
behavior. See [adaptive retry scheduling](event-acknowledgment-node-retry-policy.md).
The shared schema and saved-callback fences are unchanged.

Delivery verifies saved token/action hashes, uses the original token and exact
JSON/reason, never renews the platform lease and never executes business code.
Remaining database lease time minus elapsed local time bounds HTTP. Settlement
checks the opaque claim token, state and database expiry. Lost/expired/superseded
claims cannot settle another worker's row. A successful callback followed by an
uncertain settlement can be repeated using the same inputs and server idempotency.
There is no PostgreSQL/HTTP transaction; local supersession cannot cancel an
already in-flight callback. The platform attempt/token fence protects replacement
claims.

`deliver` and `recoverNext` return the committed **local status** after callback
success, rejection or uncertainty. Inspect it: `unconfirmed` schedules a later
attempt and `superseded` stops this row. They throw for invalid input, storage or
settlement failure, an empty queue, or a lost recovery claim. A thrown error may
leave an active claim until expiry; do not assume callback failure or settlement.
Cancellation is cooperative and active driver calls still require driver timeouts.
One claim object permits only one concurrent delivery operation.

## Diagnostics and retention

For bounded backlog counts, expired claims and overdue ages, use the read-only
[Node recovery health summary](event-acknowledgment-intent-health.md#node-summary).
It does not claim work or execute callbacks.

`listEventAcknowledgmentIntents(pool, accountId, appId, { invocationId, limit, signal })`
returns at most 100 retained metadata rows, default 25. Omit `invocationId` to list
the owner scope. Replay generations remain decimal int64 strings in diagnostics;
recovery rejects generations above `Number.MAX_SAFE_INTEGER`, matching the live
Node worker's generated-response limitation. Use a compatible recovery host for
such rows; Node never rounds or silently skips their fence.

| State | Meaning |
| --- | --- |
| `pending` | Saved callback is due or leased for recovery. |
| `unconfirmed` | Callback is uncertain; protected inputs remain, claim is released, next due time uses database time plus backoff. |
| `confirmed` | Matching platform callback succeeded and local settlement committed. |
| `superseded` | Newer lineage, unavailable platform claim or a committed receipt blocks this intent. |

Terminal rows clear raw platform tokens, result bodies and recovery claims, while
retaining diagnostic digests. In-memory claims and backups can still contain
secrets. Delivery attempts count local claims and saturate at 2,147,483,647.
No automatic polling, retention or pruning is installed. Keep receipts throughout
the retry/replay horizon and intent metadata while stale jobs/restores can return;
deleting the newest row removes the local supersession fence. Consumer deliveries
remain independent. No ordering or external exactly-once guarantee is added.

See the [durable integration example](../examples/event-acknowledgment-node/README.md).
Build/generation/static validation is local; runtime concurrency, restart and
cross-SDK database behavior require hosted acceptance before release.

For existing active unbound/v1 backlog, use the separate
`adoptEventAcknowledgmentIntentRetryBudget` command. Install its optional receipt
schema, select precise metadata, authorize and durably save the original command,
then apply timestamp/count/policy guards. It rejects live claims/newer attempts and
preserves v1 timing, callbacks, counts and due times. See
[Node budget adoption and replay](event-acknowledgment-budget-adoption.md#node-selection-adoption-and-replay).
