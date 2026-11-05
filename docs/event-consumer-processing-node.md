# Node consumer processing leases

Node supports application-owned PostgreSQL leases for side-effect-free preparation
followed by a short business transaction. The claim belongs to one account, app,
subscription and durable acceptance; each consumer remains independent. The schema,
identity/input digests and database-clock fencing follow the
[Go processing protocol](event-consumer-processing.md).

## Install and claim

Explicitly install the exported `eventConsumerReceiptSchema` and
`eventConsumerProcessingSchema` in the application database. The latter is packaged
byte-for-byte from the existing Go DDL. Helpers never install schemas, prune rows
or schedule retries. Protect table/backup access: the processing row contains the
opaque claim token. Configure bounded acquisition, query and statement timeouts.

After authenticating and authorizing the delivery route, preserve exact original
bytes and use trusted owner configuration:

```js
import {
  eventConsumerRequestFromHeaders, claimEventConsumerProcessing,
} from '@gregale/sdk-node/preview';

const input = eventConsumerRequestFromHeaders(headers, originalBody, accountId, appId);
const lease = await claimEventConsumerProcessing(pool, input, 30000, { signal });
let prepared;
if (!lease.completed) {
  prepared = await prepareWithoutEffects(originalBody, signal);
  await lease.renew(pool, 30000, { signal });
}
const receipt = await lease.complete(pool, async tx => {
  await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [invoiceId]);
  return '{"applied":true}';
}, { signal });
// Return the exact receipt.body only after confirmed completion.
```

Preparation may read, validate or calculate, but it must not create external or
uncommitted business effects. For longer preparation, renew with a safe margin,
serialize renewals, and stop on any renewal failure. The helper starts no renewal
loop. Before completion, stop and join local renewal work; completion holds the
processing row lock, so renewal cannot extend it during the transaction.

`durationMs` is required, positive whole milliseconds, no larger than
9,223,372,036,854 (the largest whole-millisecond Go duration). Use a duration
appropriate to the host's deadline and leave time for acquisition, queries and
completion. This is a local lease bound, separate from platform acknowledgment
policy. Expiry uses database time; sleeping or a local timer is not proof that a
claim is available. An expired row may be taken over in place with a new token,
while its original input digest remains immutable. A busy claim is not successful
processing and must not produce an early success response.

The lease is a process-local capability. String/inspection/JSON output redacts it,
and it cannot be reconstructed from formatted output or a saved token. Reclaim
using the authenticated original request after restart. The read-only `completed`
property means claim observed a matching receipt; it is not acknowledgment proof
and is not updated by later operations. Skip preparation when true, then complete
to validate and replay the stored result. Renewing a completed claim normally
reports lease loss because no processing row is needed.

## Fence the final transaction

Completion uses the existing consumer identity lock and checks the receipt first.
Without a receipt it locks the processing row, validates token/input/expiry, then
runs the short callback. It inserts the consumer receipt and deletes only the
same still-unexpired claim in that transaction. A lost or expired fence rolls
back writes and receipt together. Replacement claims wait for that transaction,
then replay its committed receipt or take over after rollback.

The conditional delete is the lease completion's linearization point. As in Go,
it occurs after receipt insertion and before any optional acknowledgment-intent
hook. Expiry after that check does not undo the transaction; COMMIT acknowledgment
may arrive later. An intent-hook failure still rolls back writes, receipt and
claim deletion. Keep the entire transaction short, including local intent staging.

An already committed receipt replays without requiring a live processing row, even
on an old capability. It still checks full owner/consumer identity, original input
and saved JSON. Deleted/mismatched receipts cannot authorize new work using an
expired or replaced token. Receipt retention remains the business-write
deduplication boundary.

## Durable acknowledgment integration

Construct `worker` from the same authenticated headers/body and owner scope as
`input`, as in the [Node acknowledgment guide](event-acknowledgment-node-worker.md).
Install `eventAcknowledgmentIntentSchema` as well for durable mode:

```js
const queued = await worker.runDurableWithProcessingLease(pool, lease,
  async (tx, workSignal) => {
    workSignal.throwIfAborted();
    await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [invoiceId]);
    return '{"applied":true}';
  }, { signal });
// Receipt, writes, processing-row removal and completion intent committed together.
// queued does not mean the platform callback is confirmed.
```

`runWithProcessingLease` provides the immediate callback alternative. Both reject
a capability bound to different consumer/input digests before database or platform
calls, serialize operations on that capability, and use the worker's existing
platform lease confirmation/renewal during new transactional work. A committed
receipt skips the callback and local fence, then stages/completes its saved result.
The initial receipt-only probe never checks an absent processing row or runs
business code. Durable recovery sends the intent without repeating preparation.

The local processing lease and platform acknowledgment lease protect separate
boundaries. Local renewal does not renew the platform claim, and the two
`WithProcessingLease` worker methods do not renew either lease while application
preparation runs outside them. Bound
preparation to the remaining platform budget, or separately maintain its platform
lease using the existing protocol and trusted host ownership. If that attempt
expires, final new business work must fail platform renewal and wait for a valid
redelivery. HTTP 202 and these helpers do not keep a parked VM awake.

For coordinated renewal during preparation, use the separate
[`runPreparedDurable` lifecycle](event-acknowledgment-node-preparation.md). It
reconciles receipts first, confirms the platform capability, claims the local
lease and maintains both during bounded side-effect-free preparation. It joins
local renewal before the same atomic receipt/intent completion while platform
renewal continues. The low-level lease helpers retain their manual lifecycle.

Use leased processing consistently for a consumer; the unleased receipt and
worker methods do not consult this optional table. The
[explicit version 3 preparation example](../examples/event-acknowledgment-node/README.md#prepare-with-a-processing-lease)
avoids silently treating earlier unleased jobs as leased. External effects still
need an outbox or their own idempotency protocol.

## Failure and cancellation

`EventConsumerProcessingError` provides bounded codes without SQL diagnostics,
raw rejected rows or capability data:

| Code | Meaning |
| --- | --- |
| `event_consumer_processing_busy` | Another unexpired claim owns preparation. |
| `event_consumer_processing_lease_lost` | No matching unexpired row can renew or complete new work. |
| `event_consumer_processing_operation_busy` | Another operation is using this same capability object. |
| `event_consumer_processing_store` | A local processing storage operation failed. |

An immutable receipt/input conflict uses `EventConsumerConflictError`. Invalid
arguments or a mismatched worker capability throw `TypeError`. Business callback
errors remain application-owned. Durable worker storage failures use the existing
sanitized intent error path; local fence loss retains the processing error code.
A per-object operation guard is not distributed job ownership. Keep exclusive,
ownership-fenced durable jobs and stop preparation after renewal loss; an expired
worker can still be physically running even though it cannot commit through this
protocol.

Cancellation is cooperative between queries and before COMMIT. Active calls are
awaited before rollback/release; configure driver timeouts. Once COMMIT is sent,
a successful response remains confirmed even if cancellation arrives, and a failed
response preserves `EventConsumerCommitUnknownError` with a sanitized cause.

An uncertain **claim** COMMIT returns no capability. Retry claim with the same
original request; it can remain busy until the possibly committed claim expires.
An uncertain **renewal** must stop preparation until its ownership is safely
reconciled; it does not establish rollback or grant a fresh lease. After an
uncertain **completion** COMMIT, use receipt-only reconciliation or repeat
completion with the same capability/request. A committed receipt skips business
code; without it the local fence must still be valid. In durable mode,
`worker.saveCompletionIntent` reconciles a saved receipt without preparation.
Do not infer rollback or automatically submit retry/dead-letter from uncertainty.

Failed preparation leaves the row to expire; no automatic release/dead-letter is
performed. Expiry alone schedules nothing. Retain receipts and immutable claims
while retry/replay remains possible. Local validation is build, schema generation,
packaging and static checks; hosted acceptance is required for timing, takeover,
concurrency, rollback and shared Go/Node behavior before release.

## Inspect retained recovery evidence

[`inspectEventConsumerProcessing`](event-consumer-processing-inspection.md) reads
one original consumer request's receipt/claim metadata with a single database
snapshot and clock. It reports completion evidence, active/expired/absent claims
and conflicts without selecting tokens or result bodies. Inspection grants no
ownership or acknowledgment proof; use the existing fenced recovery paths.
