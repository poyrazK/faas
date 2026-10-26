# Coordinated Python event preparation

`AsyncEventAcknowledgmentWorker.run_prepared_durable` maintains the application
processing lease and Gregale acknowledgment lease during bounded side-effect-free
preparation. It then commits business writes, consumer receipt, processing-claim
removal and completion intent together. A separate
[intent recovery host](event-acknowledgment-python-intents.md) delivers the callback.
The lifecycle follows [Node preparation](event-acknowledgment-node-preparation.md).

Use an authenticated delivery, trusted owner configuration, protected original
bytes/headers and exclusively owned durable jobs as in the
[Python worker guide](event-acknowledgment-python-worker.md). Explicitly install
`event_consumer_receipt_schema`, `event_consumer_processing_schema` and
`event_acknowledgment_intent_schema`. No platform endpoint, migration, automatic
schema installation or service is added.

## Prepare and complete

```python
from faas_sdk.preview import EventAcknowledgmentPreparedOptions

def connection_scope():
    # Fresh exclusive idle autocommit async connection scope for each call.
    return application_pool.connection(timeout=3)

async def prepare(cancel):
    cancel.check()
    value = await read_and_validate_without_effects(original_body, cancel)
    cancel.check()
    return value

async def apply(cursor, cancel, prepared):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (prepared.invoice_id,))
    cancel.check()
    return b'{"applied":true}'

queued = await worker.run_prepared_durable(
    connection_scope, prepare, apply,
    EventAcknowledgmentPreparedOptions(
        preparation_timeout_ms=60_000, processing_lease_ms=30_000,
    ),
    stop_event=job.stop_event,
)
# Ack the fenced durable job only after confirmed local commit.
# queued.intent is durable; platform callback delivery is still independent.
```

The zero-argument synchronous factory returns an async context manager, rather
than a coroutine or bare connection. Every scope must supply an exclusively leased
idle psycopg async connection with `autocommit=True`. It must finish bounded
acquisition/cleanup, discard closed connections and never suppress operation
errors. A failing cleanup must retain exclusive connection ownership for the
SDK's close attempt; it must not release the connection for reuse and then raise.
Separate scopes serve reconciliation, claim, renewals and completion.
No business transaction stays open during preparation. Do not share a connection
across concurrently active operations. Bound driver/statement calls as well as
pool timeouts; the SDK does not install a driver or forcibly interrupt SQL or
connection acquisition/cleanup.

Preparation may read, validate and calculate but must not perform business or
external effects, control transactions or leave detached work running. Honor the
read-only cancellation view, join owned work before returning and yield during
long CPU work so timers can run. Prepared values stay process-local and pass
directly to `handler(cursor, cancel, prepared)`; the SDK never stores/logs them.
Use only the supplied cursor and transactional SQL in the short final handler.
External effects still require an outbox or idempotency protocol.

## Lifecycle and lease boundaries

1. Reconcile the receipt and stage its completion intent first. An existing
   matching receipt skips preparation, business code, processing-table access and
   platform renewal. This method sends no terminal callback.
2. If missing, start the preparation budget, confirm the exact platform token,
   invocation, attempt and replay generation, then start platform renewal. A
   read-only status response never grants admission by itself.
3. Claim the local processing row and renew it once before preparation. A busy
   claim invokes neither callback. If claim observes a concurrently committed
   receipt, stage that receipt without preparation.
4. Renew local processing serially at `processing_lease_ms // 3` and platform
   acknowledgment serially at one third of its advertised duration, in separate
   loops. A slow SQL renewal does not serialize platform HTTP renewal. Failed or
   unconfirmed renewal signals cancellation and prevents new work.
5. After preparation returns, stop the local loop and join active renewal. Refresh
   the local lease once, check the budget and stop the preparation timer. Platform
   renewal continues into completion without a second monitor.
6. Complete through the existing
   [processing fence](event-consumer-processing-python.md#fence-the-short-transaction).
   Stop and join owned renewal work before returning on success or failure.

Stopping local renewal interrupts waits, never active SQL. Calls finish their
transaction and scope cleanup before handoff. Completion locks the local row
before new business work and conditionally deletes the same unexpired claim after
receipt insertion. It does not renew while holding that lock. The delete is the
local linearization point, before intent staging and COMMIT; later expiry does
not undo a confirmed transaction. A receipt committed by another worker replays
without invoking the business handler.

Local and platform attempt/token fences protect separate boundaries. Platform
renewal provides cooperative admission/cancellation, not a distributed transaction
with application SQL or a guarantee that remote expiry cannot race a local commit.
Receipts retain the business deduplication boundary, and each consumer keeps its
independent acceptance identity.

## Budgets and shutdown

`EventAcknowledgmentPreparedOptions` is frozen and validated before I/O:

| Option | Meaning |
| --- | --- |
| `preparation_timeout_ms` | Required native integer, 1–2,147,483,647 ms. Covers platform startup, claim, preparation, local handoff and final refresh, including acquisition/cleanup. |
| `processing_lease_ms` | Native integer, 3–2,147,483,647 ms; default 30,000. Local renewal waits `duration // 3` after each completed scope. |
| `on_event` | Optional keyword-only synchronous, nonblocking observer of [bounded lifecycle metadata](event-acknowledgment-python-telemetry.md). Observer exceptions are isolated. |

Reject booleans/fractional values and snapshot effective options for the call.
`stop_event` is an optional `asyncio.Event` covering initial reconciliation, final
transaction and cleanup as well as preparation. Native cancellation has the same
cooperative ownership behavior.

The preparation budget starts after the initial probe and scope cleanup and ends
before final completion. Monotonic checks at phase boundaries catch delayed timer
callbacks. It is not a hard SQL/process kill. Use a parent deadline and bounded
driver calls for completion/shutdown. Leave lease margin appropriate to latency;
minimum accepted values are not an operational recommendation.

Cancellation signals preparation and joins it, active SQL, acquisition/cleanup
and renewals. A callback or driver ignoring deadlines can delay shutdown and lose
its lease. Timers cannot renew on a blocked event loop or paused host. HTTP 202
does not keep a parked VM awake. Reconstruct the original protected job after a
crash; recompute preparation only if no receipt exists and a new claim is admitted.

## Failure and reconciliation

| Outcome | Owner action |
| --- | --- |
| `event_consumer_preparation_timeout` | Await stopped work, retain the job and apply the owner's explicit recovery policy. |
| Local busy/lost fence | Reclaim original input only under exclusive job ownership; no stale capability may commit new work. |
| Platform unconfirmed/lost renewal | Stop new preparation/work. Preserve the original attempt, never another attempt's token. |
| Unknown claim/renewal COMMIT | Stop without inferring rollback. A possibly committed claim can remain busy until expiry. |
| Unknown final COMMIT | Reconcile with `save_completion_intent` on a fresh connection and the same original job. |
| Confirmed local commit | Writes, receipt, claim removal and intent are durable; recovery owns callback delivery. |

Claim/renewal/final `EventConsumerCommitUnknownError` takes precedence over timeout,
cancellation or another callback failure, including uncertainty discovered while
joining renewal. Unknown-commit connections are closed and must be discarded.
Confirmed final commits remain confirmed after late renewal loss, cancellation or
scope cleanup failure. Failed scope cleanup attempts to close the connection
without erasing commit evidence. Drain operations before closing clients/pools/loops.

Factory acquisition/cleanup errors use fixed storage errors without raw driver
diagnostics. Local/platform/conflict/intent errors retain existing bounded paths.
Preparation/business errors remain application-owned; sanitize those before public
logging and never log inputs, capabilities or prepared data.

No failure automatically selects retry/dead-letter, releases claims, deletes
receipts or schedules work. Failed preparation leaves the row to expire. Retain
protected jobs until the correct fenced handoff succeeds. The worker guard spans
reconciliation, preparation, completion and cleanup; separate workers still need
exclusive distributed ownership. Use leased processing consistently and retain
receipts across retry/replay.

The [explicit version-5 example](../examples/event-acknowledgment-python/README.md#coordinate-preparation-and-both-leases)
selects this lifecycle without reinterpreting older jobs. Local validation covers
compilation/lint, Python 3.11 syntax, generation, packaging and static checks. No
tests/assertions are added/changed and no local tests run. Hosted concurrency,
lease loss, expiry/takeover, callbacks, rollback, cancellation and uncertain-commit
qualification remains pending before release.

For read-only diagnostics after busy or interrupted preparation, use
[`ainspect_event_consumer_processing`](event-consumer-processing-inspection.md#python-inspection).
Its metadata snapshot grants no ownership, validates no saved result and never
chooses recovery. Unknown commits still require the existing receipt protocol.

For process-local phase/outcome diagnostics, attach the optional
[`on_event` observer](event-acknowledgment-python-telemetry.md). Events contain fixed
metadata only and never select recovery. Confirmed/unknown commit outcomes retain
their existing precedence; shutdown follows joined coordinator renewal cleanup.
