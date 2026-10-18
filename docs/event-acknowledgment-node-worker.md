# Node event acknowledgment worker

`EventAcknowledgmentWorker` joins the existing application-owned PostgreSQL
consumer receipt to Gregale's deferred delivery acknowledgment. It renews the
platform lease while business writes run, then completes using the exact saved
JSON result. Configure `FaaSClient` with `apps:read` and `deploy:write` (or admin)
credentials before using it, and explicitly install `eventConsumerReceiptSchema`
in the application database.

The application supplies durable job storage, exclusive job ownership and a
recoverable worker that can reach apid. A returned HTTP 202 does not keep a VM
awake. Authenticate and authorize the internal delivery route before parsing
headers; these headers are not signatures.

## Accept and reconstruct

```js
import { eventAcknowledgmentWorkerFromHeaders } from '@gregale/sdk-node';

const worker = eventAcknowledgmentWorkerFromHeaders(
  appSlug, originalHeaders, originalBody, accountId, appId,
);
```

The app slug and owner UUIDs come from trusted configuration. Preserve the exact
original body and event identity, invocation attempt/replay context, acknowledgment
token and advertised lease headers in protected job storage **before** returning
202. Do not store bearer credentials with the job. Reconstruct with the same
factory after a restart. Keep each consumer's subscription identity separate.
Worker formatting, inspection and JSON serialization redact its capability and
are not a persistence format. Trusted custom HTTP transports can observe requests.

The factory validates canonical nonzero scope/invocation UUIDs, matching CloudEvent
source/ID, positive decimal acceptance identity, attempt/lineage metadata, a single
lowercase 64-hex token and a 1–300 second advertised lease. Duplicate case aliases
and multiple header values are rejected. A single-value array is accepted.
Acceptance IDs remain decimal strings. The current generated acknowledgment status
uses JavaScript numbers, so this helper rejects replay generations above
`Number.MAX_SAFE_INTEGER` instead of rounding a fencing value.

## Process and complete

```js
const result = await worker.run(pool, async (tx, signal) => {
  signal.throwIfAborted();
  await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [invoiceId]);
  return JSON.stringify({ applied: true });
}, { signal: job.signal });
// Only now remove/acknowledge the owned durable job.
```

The callback runs inside `withEventConsumerTransaction`; use only the supplied
transaction, return JSON text, and leave commit/rollback to the SDK. Keep it short.
External effects require an outbox or their own idempotency protocol. The receipt
identity remains account, application, subscription and durable acceptance, so a
different consumer processes and recovers independently.

A committed receipt skips business code and can be completed even when the old
platform lease can no longer be renewed. Without a receipt, a successful renewal
must confirm the exact invocation, attempt and replay generation before business
code starts. A 409 during handoff permits a bounded retry only while a scoped GET
still reports `delivering`; that GET never authorizes processing by itself.

Renewal runs serially at approximately one third of the advertised lease. Its
first unconfirmed result stops renewal and aborts the callback's signal. Renewal
stops and joins before the terminal callback. A confirmed receipt can still be
submitted after a last renewal failure; the platform token/attempt fence decides
whether it applies. Completion transmits saved JSON without parsing/stringifying
it, preserving large numbers and JSON text across callback retries.

Cancellation is cooperative. The receipt helper checks the signal before/after
queries, after the callback and immediately before sending COMMIT. It waits for
active driver calls before rollback/releasing a connection; it does not interrupt
SQL already executing. Configure pool acquisition, query and PostgreSQL statement
timeouts. Once COMMIT was sent, an abort cannot establish rollback: success returns
the confirmed receipt and a commit error remains `EventConsumerCommitUnknownError`.
A handler that ignores cancellation can delay shutdown. Renewal is not a database
fence, and lease expiry cannot undo already committed effects.

## Recover and choose a disposition

`EventAcknowledgmentWorkerError` exposes a bounded `code`, optional HTTP `status`
and `result`. Callback errors do not retain remote bodies, generated requests or
transport causes. Its JSON/inspection output omits receipt/result bodies. Database
and business callback errors remain application-owned; avoid logging raw errors or
protected jobs. A false `receiptConfirmed` does not prove rollback.

| Outcome | Recovery |
| --- | --- |
| Receipt confirmed, completion unconfirmed | Persist the job and call `worker.completeReceipt(pool, { signal })` with the same original delivery. It never invokes business code. |
| `EventConsumerCommitUnknownError` | Use `completeReceipt` to reconcile the original identity; do not infer rollback or automatically reject the delivery. |
| `event_consumer_receipt_missing` | No matching committed receipt was observed after serialization. This does not grant exclusive processing ownership; follow the job owner's known-rollback policy. |
| `event_acknowledgment_lease_lost` | Stop work. A redelivery reconstructs a worker with its new token; the old worker never adopts it. |
| Receipt conflict | Investigate the immutable input/identity mismatch; do not overwrite or delete the receipt. |
| Known rollback, stopped work, exclusive job ownership | Persist an explicit retry/dead-letter action and reason before invoking it; repeat the same decision after uncertainty. |

```js
await worker.retry(pool, 'dependency_unavailable', { signal });
// Or an explicitly chosen terminal disposition:
await worker.deadLetter(pool, 'invalid_business_input', { signal });
```

Reasons are 1–128 lowercase ASCII letters/digits or `_.-`. Both methods first
check the receipt. A committed receipt returns `event_consumer_receipt_committed`
and confirmed receipt data without sending a negative acknowledgment; complete it
instead. Business exceptions never automatically select retry/dead-letter. A
receipt-absence observation is not a distributed processing lock. Separate worker
instances/processes require exclusive durable-job ownership; overlapping operations
on one object fail with `event_acknowledgment_worker_busy`.

## Callback budgets

Factory options are captured at construction:

- `callbackAttempts`: 1–10 calls, default three including the first.
- `callbackTimeoutMs`: whole milliseconds from one through `min(5000, lease/6)`,
  with that maximum rounded down as the default.
- `retryBackoffMs`: whole milliseconds from one through `lease/3`; default
  `min(100, lease/12)` rounded down.

Startup and terminal callbacks retry transport failures and HTTP 408/425/429/5xx
with unchanged inputs. Valid delta-seconds/IMF-fixdate Retry-After hints and numeric
Problem `retry_after_seconds` are honored within the lease/3 wait horizon. Invalid
or longer hints stop helper retries for the durable owner's scheduling policy.
Live renewal stops on its first failure. Each operation accepts an optional
`AbortSignal`; use a parent deadline to bound the entire operation. The configured
SDK transport may retry within each RPC deadline. Configure its retry policy
appropriately when the helper should own the retry budget.

For atomic callback persistence, use [durable Node acknowledgment intents](event-acknowledgment-node-intents.md).
`runDurable` commits the intent with the receipt and business writes; a separate
recovery host delivers it. The [durable-job integration module](../examples/event-acknowledgment-node/README.md)
uses that mode for new jobs. Keep existing immediate-mode jobs on their original
recovery protocol; do not mix modes for the same attempt. Neither mode installs
schemas or background services. For a local preparation fence, use the separate
[Node processing lease](event-consumer-processing-node.md) and the worker
`runWithProcessingLease` / `runDurableWithProcessingLease` methods. See the
[Go worker](event-acknowledgment-worker.md) and [platform protocol](event-deferred-acknowledgments.md)
for shared boundaries.

For preparation that needs both leases maintained, use
[`runPreparedDurable`](event-acknowledgment-node-preparation.md) with an explicit
preparation timeout. It manages receipt-first recovery, local claim and dual
renewal, then joins local renewal before the fenced atomic receipt/intent commit.
Preparation remains side-effect-free and cooperative; use a parent deadline and
bounded driver calls for the final transaction and shutdown.
