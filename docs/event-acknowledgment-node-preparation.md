# Coordinated Node event preparation

`EventAcknowledgmentWorker.runPreparedDurable` manages a local processing lease
and Gregale's acknowledgment lease while side-effect-free preparation runs outside
the business transaction. It then commits business writes, consumer receipt,
processing-claim removal and completion intent together. A separate
[intent recovery host](event-acknowledgment-node-intents.md) delivers the callback.

Use an authenticated delivery, trusted owner scope, protected original inputs and
exclusive durable-job ownership as in the [Node worker guide](event-acknowledgment-node-worker.md).
Explicitly install `eventConsumerReceiptSchema`, `eventConsumerProcessingSchema`
and `eventAcknowledgmentIntentSchema` in the application database. The coordinator
uses the existing Go/Node schemas and platform endpoints; it installs no service,
retry scheduler or schema.

## Run preparation and a short final transaction

```js
const result = await worker.runPreparedDurable(pool,
  async signal => {
    signal.throwIfAborted();
    return await readAndValidateWithoutEffects(originalBody, signal);
  }, async (tx, signal, prepared) => {
    signal.throwIfAborted();
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [prepared.invoiceId]);
    return '{"applied":true}';
  }, {
    preparationTimeoutMs: 60000,
    processingLeaseMs: 30000,
    signal: job.signal,
  });
// Ack the owned durable job only after this confirmed local commit.
// result.intent is durable; platform callback delivery is still independent.
```

Preparation may read, validate and calculate. It must not commit business effects,
send external effects or start detached work. Its return value is process-local
and passes directly to the final callback; the SDK never stores or logs it. Join
all work before returning, observe cancellation and yield during long CPU work so
renewal timers can run. External effects still need an outbox or their own
idempotency protocol. The final callback uses only the supplied transaction and
returns exact JSON text.

## Lifecycle and lease boundaries

1. Reconcile the consumer receipt and stage its completion intent first. An
   existing receipt skips preparation, business code, processing-table access
   and platform renewal. No terminal callback is sent by this method.
2. If missing, start the preparation budget, confirm the exact platform token,
   invocation, attempt and replay generation, and begin platform renewal. A
   read-only status response never authorizes preparation by itself.
3. Claim the application-owned processing row and renew it once before invoking
   preparation. A busy claim fails without invoking either callback. If claim
   observes a concurrently committed receipt, reconcile it without preparation.
4. Renew the local lease serially at one third of its configured duration, and
   the platform lease serially at one third of its advertised duration. They are
   separate loops; a slow database call does not serialize the platform loop.
   The first failed or unconfirmed renewal signals cancellation and stops work.
5. After preparation returns, stop the local loop and await any active renewal.
   Refresh the local lease once more for a completion margin, check the budget,
   then stop the preparation timer. Platform renewal continues through the final
   transaction; no second platform monitor is started during the handoff.
6. Use the same [processing fence](event-consumer-processing-node.md#fence-the-final-transaction)
   for the final writes and atomic receipt/intent commit. Stop and join owned
   renewal work before returning, whether the operation succeeds or fails.

An active local renewal is awaited when stopping the loop; a graceful handoff
does not cancel that SQL call or ignore its outcome. The final transaction
checks the local token/expiry before business code and at conditional claim
removal. It never renews the local row while holding that row's lock. A receipt
committed by another worker is replayed, with the business callback skipped.

The local completion linearization point is the conditional claim removal,
before intent staging and COMMIT. Later expiry does not undo that transaction.
Platform renewal is cooperative admission/cancellation, not a distributed SQL
transaction or a guarantee that remote expiry cannot race a local commit.
Receipt deduplication and the platform attempt/token fence retain their existing
boundaries. Each consumer keeps its independent acceptance/receipt identity.

## Budgets and shutdown

`EventAcknowledgmentPreparedOptions` accepts:

| Option | Meaning |
| --- | --- |
| `preparationTimeoutMs` | Required whole milliseconds, 1–2,147,483,647. Covers platform startup, processing claim, preparation, joining local renewal and the final local refresh. |
| `processingLeaseMs` | Whole milliseconds, 3–2,147,483,647; default 30,000. Local renewal waits `floor(duration / 3)` between completed calls. |
| `signal` | Optional parent cancellation/deadline for the whole operation, including receipt reconciliation and the final transaction. |
| `onEvent` | Optional synchronous, nonblocking observer of [bounded lifecycle metadata](event-acknowledgment-node-telemetry.md). Observer failures are isolated. |

The preparation budget starts after the initial receipt-only probe and ends before
the final transaction. A timer aborts preparation, with monotonic budget checks at
phase boundaries to catch delayed timers. The timeout is not a hard SQL or process
kill. A committed receipt can still replay even when preparation would no longer
be admitted. Use a parent deadline and bounded pool acquisition, driver calls and
PostgreSQL statements to bound the final transaction and shutdown. Leave a local
lease margin appropriate to actual latency; the minimum accepted duration is not
an operational recommendation.

Cancellation awaits preparation and active driver calls. A callback that ignores
its signal can delay shutdown and lose the lease; it cannot be forcibly stopped
by the helper. Timers cannot renew while the event loop is blocked or the host
is paused. HTTP 202 does not keep a parked VM awake. Use a recoverable host and
reconstruct the original job after a crash; prepared data is recomputed when no
receipt exists and a new claim is admitted.

## Failure and recovery

| Outcome | Owner action |
| --- | --- |
| `event_consumer_preparation_timeout` | Preparation budget expired. Await stopped work; retain the job and apply the owner's recovery policy. |
| Local claim busy or local fence lost | No new work can commit through that capability. Reconstruct and reclaim only under exclusive job ownership. |
| Platform renewal unconfirmed/lost | Stop preparation and new work. Preserve the original attempt; do not adopt another attempt's token. |
| Unknown claim or renewal COMMIT | No rollback is established. Stop work; an uncertain claim can remain busy until expiry. |
| Unknown final COMMIT | Reconcile with `worker.saveCompletionIntent` using the same original request; never infer rollback or automatically reject. |
| Confirmed local commit | Business writes, receipt and completion intent are durable. The recovery host owns callback delivery, including after a last platform renewal failure. |

The coordinator preserves `EventConsumerCommitUnknownError` from claim, renewal
or final completion even if cancellation arrives. A successful final COMMIT
remains confirmed; cleanup does not turn it into a timeout or lease-loss error.
Storage and platform errors keep the existing bounded, sanitized error paths;
preparation/business errors remain application-owned. Do not log protected inputs,
prepared data, raw application errors or capabilities.

No failure automatically selects retry/dead-letter, deletes a receipt or releases
a processing row. Failed preparation leaves the claim to expire. The worker's
operation guard spans receipt reconciliation, acquisition, preparation, commit
and cleanup, but separate instances still require exclusive durable-job ownership.
Use the leased protocol consistently for a consumer and retain receipts while
retry/replay remains possible.

For live phase and duration diagnostics, use optional
[`onEvent` lifecycle telemetry](event-acknowledgment-node-telemetry.md). Events
contain fixed metadata only and do not choose recovery. Confirmed local
receipt/intent commit remains confirmed after a late renewal failure; shutdown
is emitted after owned renewal work joins. Keep observers passive and nonblocking.

For read-only diagnostics after a busy or interrupted job, use
[`inspectEventConsumerProcessing`](event-consumer-processing-inspection.md).
It observes local receipt/claim metadata without selecting tokens/results or
granting ownership. Unknown commits still require receipt reconciliation through
the existing completion path; an absent/expired observation does not prove rollback.

The [version 4 example](../examples/event-acknowledgment-node/README.md#coordinate-both-leases-during-preparation)
explicitly selects this lifecycle while leaving earlier job versions on their
original paths. Local validation covers build, generation, packaging and static
checks. Hosted acceptance remains required for lease loss, expiry/takeover,
concurrency, cooperative cancellation, rollback and uncertain commits before release.
