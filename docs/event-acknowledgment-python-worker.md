# Python async deferred event acknowledgment worker

`AsyncEventAcknowledgmentWorker` coordinates platform lease renewal, atomic
consumer receipts and immediate terminal callbacks. It uses the existing
[deferred acknowledgment protocol](event-deferred-acknowledgments.md) and
[application-owned consumer receipt table](event-consumer-transactions.md).

For independently claimed local preparation fences, use
[`run_with_processing_lease`](event-consumer-processing-python.md#compose-with-acknowledgment-workers)
or its durable counterpart. Receipt replay skips the local claim; new transactional
work requires its current unexpired fence. Applications own preparation and local
renewal separately from the worker's platform acknowledgment lease.

[`run_prepared_durable`](event-acknowledgment-python-preparation.md) coordinates both
lease lifetimes during bounded side-effect-free preparation, then stages an atomic
receipt/intent completion. It uses fresh connection scopes rather than holding
a business transaction open during preparation.

For atomic transfer of callback responsibility to an independent recovery host,
use [durable completion intents](event-acknowledgment-python-intents.md). The
immediate mode documented here retains its original ingestion-job contract.

## Configure and retain original inputs

Configure one SDK client with `apps:read` plus `deploy:write`, or `admin`. Pass
its generated authenticated `client.inner` to the worker factory. Authenticate
and authorize the internal delivery before constructing the worker: headers and
the opaque token do not establish application authority by themselves.

```python
from faas_sdk import AsyncEventAcknowledgmentWorker, FaaSClient

client = FaaSClient(base_url=api_url, token=worker_api_token)
worker = AsyncEventAcknowledgmentWorker.from_headers(
    client.inner, app_slug, original_headers, original_body, account_id, app_id,
)
```

Persist exact original bytes, the required delivery headers and trusted owner
scope in a protected durable job before responding HTTP 202. Headers must retain
duplicate values/case aliases so validation can reject ambiguous context; adapt
framework multi-value headers explicitly. Tokens are 64 lowercase hex characters
and advertised leases are canonical integer seconds from 1 through 300. Factory
validation captures acceptance/consumer identity and exact native integer attempt
and replay generation without floating-point conversion.

Restrict/encrypt job, backup and queue-console access. Do not log original records,
headers or bodies. Worker repr/string output is redacted and copying/pickling is
rejected; reconstruct it from the owned protected delivery after restart. These
representations do not make debugger locals or transport hooks safe to publish.

Explicitly install `event_consumer_receipt_schema` as the application database
owner. Each operation needs an exclusively leased idle psycopg async connection
with `autocommit=True`. Configure bounded pool acquisition, query and PostgreSQL
statement/lock timeouts. The SDK adds no driver dependency, schema installation,
queue, host lifecycle, timer service or receipt pruning.

## Process and complete

```python
async def apply(cursor, cancel):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    cancel.check()
    return b'{"applied":true}'

result = await worker.run(connection, apply, stop_event=job.stop_event)
# The local receipt and platform terminal callback are confirmed.
await job.ack()  # The application's adapter must independently fence job ownership.
```

Use only transactional SQL through the provided cursor; never commit, roll back
or change transaction lifecycle in the callback. Return bounded valid JSON bytes
or text. Business writes and the receipt commit together. External effects need
an outbox or their own idempotency protocol. Acceptance plus consumer remains the
business deduplication identity; attempts/tokens do not replace it.

The worker first serializes on that identity and probes for a saved receipt.
Existing receipts skip the handler and platform renewal and use the exact saved
JSON bytes for completion. Without a receipt, successful renewal must confirm
the matching invocation, attempt and replay generation in processing state before
business code runs. A 409 during the 202 handoff permits a bounded retry only
when a scoped GET still reports delivering. GET alone never authorizes work.

Renewal is serial at approximately one third of the advertised lease. Its first
unconfirmed result sets cancellation; the receipt transaction checks cancellation
around driver/handler awaits and before entering COMMIT. Renewal stops and joins
before completion. A confirmed receipt remains confirmed after late renewal loss.
The token/attempt fence still decides whether a later terminal callback applies.

The helper is loop-owned after its first operation. Overlapping operations on
one worker fail with `event_acknowledgment_worker_busy`; distinct objects/processes
still require exclusive durable-job ownership. Keep the durable ingestion job
until terminal confirmation in this immediate mode. Local commit alone does not
transfer callback responsibility to another host.

## Cancellation and uncertain commits

Each operation accepts an optional borrowed `asyncio.Event` named `stop_event`.
The callback receives a read-only cancellation view with `is_set()`, `wait()` and
`check()`. Owner cancellation, native task cancellation or failed live renewal
sets that view. Use it between bounded units; it cannot be cleared by the handler.

Native caller cancellation signals the owned operation instead of cancelling
its business coroutine or active SQL. The caller waits for active driver calls,
transaction rollback/commit and renewal cleanup to join. Repeated cancellation
requests keep signalling the same operation. A handler ignoring cancellation,
unbounded driver call or slow transport cleanup can delay return; a parent timeout
is a cancellation request, not a hard upper bound on database cleanup. Drain
operations before closing the event loop, connection or HTTP client.

Before COMMIT, an observed cancellation/lease failure rolls back new writes.
After COMMIT may have been sent, an error remains `EventConsumerCommitUnknownError`
and never proves rollback. A successful COMMIT preserves confirmed receipt
progress even if cancellation/renewal failure follows. A validated terminal
callback result can win a simultaneous late cancellation.

The worker closes its exclusively leased connection after an unknown COMMIT,
preserving the original unknown-outcome error even if close also fails. Return
that connection through the pool's discard path and reconcile using a fresh idle
autocommit connection. Ordinary connection/client shutdown remains application-owned.

`EventAcknowledgmentWorkerCancelled` remains an `asyncio.CancelledError` subtype
and exposes `result`; callback/lease errors expose the same progress. Inspect
`result.receipt_confirmed` and retain the owned job. False is not rollback proof.
Never log protected receipt bodies. Callback errors have fixed messages/codes,
optional bounded HTTP status and progress, without remote bodies, HTTP requests
or attached raw transport causes. Existing business/database errors remain
application-owned.

## Explicit recovery and dispositions

| Outcome | Recovery |
| --- | --- |
| Confirmed receipt, completion unconfirmed/cancelled | Retain the original owned job and call `await worker.complete_receipt(connection, stop_event=...)`. It never invokes business code. |
| `EventConsumerCommitUnknownError` | Reconcile the same original consumer identity through `complete_receipt`; the receipt lock serializes with an earlier uncertain transaction. |
| `event_consumer_receipt_missing` | No matching receipt was observed after serialization. Follow the job owner's explicit recovery policy; this supplies no processing ownership or rollback proof. |
| `event_acknowledgment_lease_lost` | Stop work and retain evidence. A redelivery constructs its own worker with its new token; old workers never adopt it. |
| Receipt conflict | Investigate immutable scope/input disagreement while preserving existing receipts. |
| Known rollback, stopped work, exclusive job ownership | Persist an explicit reason/action, then use `retry` or `dead_letter`. Repeat that same saved decision after an uncertain callback. |

```python
await worker.retry(connection, "dependency_unavailable", stop_event=job.stop_event)
# Or the owner's explicitly chosen terminal decision:
await worker.dead_letter(connection, "invalid_business_input", stop_event=job.stop_event)
```

Reasons contain 1..128 lowercase ASCII letters/digits or `_.-`. Both methods
probe the receipt first. A committed receipt raises `event_consumer_receipt_committed`
with confirmed receipt progress and sends no negative callback. Receipt absence
is an observation, not a distributed lock. No exception automatically selects
retry/dead-letter, no helper steals ownership, and no method changes the token,
attempt, replay generation, original mode or saved completion bytes.

## Callback budgets and observations

`EventAcknowledgmentWorkerOptions` captures:

- `callback_attempts`: integer 1..10, default three including the first call.
- `callback_timeout_ms`: whole milliseconds 1..floor(min(5000, lease_ms/6)),
  default that maximum. Applies as both an HTTP timeout and total callback budget.
- `retry_backoff_ms`: whole milliseconds 1..lease_ms/3, default floor(min(100,
  lease_ms/12)).

Startup and terminal callbacks can retry transport failures and raw HTTP
408/425/429/5xx with unchanged inputs. Live renewal stops on its first failure.
Valid integer Problem `retry_after_seconds`, delta-seconds or canonical IMF-fixdate
Retry-After hints are honored only within the lease/3 wait horizon. Invalid or
longer hints stop helper retries for owner scheduling. Redirects are disabled.
Status bodies are limited to 64 KiB and projected onto immutable known fields;
unknown properties are never returned. Fencing values require native JSON integers,
and timestamp comparisons preserve RFC3339 nanoseconds.

The configured SDK transport can have its own retries and logging. Configure it
appropriately for callback budgets and secret-safe logging. The standard wrapper
can turn Problem responses into exceptions without retry-hint extensions; those
errors stop helper retries rather than inventing a shorter wait. Worker response
limits apply to the stream it receives; custom/wrapper transports own any buffering
they perform first. The application owns client shutdown.

The [protected-job Python example](../examples/event-acknowledgment-python/README.md)
wires durable ingress, owned processing and receipt-only recovery. This increment
uses immediate terminal callbacks and existing receipts. Durable intent storage,
local preparation leases and preparation monitoring remain separate features.

Validation covers Python compilation/lint, isolated generation parity, wheel/sdist
inclusion, schema parity and static documentation checks. No tests/assertions were
added or changed and no local tests were run. Hosted database, handoff, callback,
renewal and cancellation qualification remains required before release.
