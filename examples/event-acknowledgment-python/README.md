# Durable Python ingestion with immediate event acknowledgment

For leased completion, see [the explicit processing mode](#complete-with-a-processing-lease).

`consumer.py` integrates the [async acknowledgment worker](../../docs/event-acknowledgment-python-worker.md)
with an application-owned protected queue. It starts no server or worker loop
and installs no schema or driver. Configure a long-lived authenticated SDK client,
explicitly install `event_consumer_receipt_schema`, and use exclusively leased idle
psycopg async connections with `autocommit=True` and bounded acquisition/query/
statement timeouts. Keep the host available through processing and callbacks;
HTTP 202 does not keep a parked VM awake or renew a lease by itself.

## Authenticate and durably ingest

After ingress authentication/authorization, trusted owner lookup and bounded
capture of exact original bytes:

```python
from consumer import accept_authenticated_delivery

response = await accept_authenticated_delivery(
    client.inner, original_headers, original_body,
    {"app": app_slug, "account_id": account_id, "app_id": app_id}, queue,
)
# Send response['status_code'] and response['body'] only after this returns.
```

The adapter's `queue.enqueue_protected(record)` must durably commit before
resolving. Failed validation/enqueue fails ingress rather than returning 202.
Preserve duplicate header values/case aliases in the framework adapter. Only
required event/invocation/acknowledgment headers enter the record; ordinary API
authorization headers are excluded. The record contains the raw acknowledgment
capability and original application body. Encrypt/restrict records, backups and
queue consoles, and never log the record. Its random job ID is not the token.

Version 1 records use the explicit `python-immediate` mode. Keep that mode and
owner scope immutable across retries. Go/Node records and other versions/modes
are rejected; do not relabel records during recovery.

## Own and process the job

The durable queue supplies an exclusively owned `job` with `record`, an
`asyncio.Event` named `stop_event`, and `async ack()`. Ownership loss or the
processing deadline sets the event. `ack()` independently fences durable deletion
against current ownership. Keep unacknowledged records across process restarts.

```python
from consumer import process_owned_delivery

async def apply(cursor, cancel, original_body):
    cancel.check()
    # Decode/validate/authorize business inputs without changing stored bytes.
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    cancel.check()
    return b'{"applied":true}'

async with application_pool.connection() as connection:
    # Pool configuration must supply idle autocommit connections.
    result = await process_owned_delivery(client.inner, job, scope, connection, apply)
```

The helper compares saved/current owner scope, validates canonical original-body
base64 and constructs the worker with the original headers. Business writes and
the receipt share one transaction. Use only the supplied cursor, cooperative
cancellation and transactional SQL. External effects require an outbox or their
own idempotency keys. A saved receipt skips business work on a later `run`.

The ingestion job is acknowledged only after terminal callback confirmation and
an ownership check. This immediate mode keeps callback responsibility in the
durable job. A local receipt commit by itself does not permit deleting the job.

## Reconcile explicitly

After an unknown COMMIT, confirmed receipt with unconfirmed callback, or owner
cancellation, retain the protected job and any already chosen recovery decision.
The owner can persist a receipt-only recovery phase through its own fenced job
update, then call:

```python
from consumer import complete_owned_receipt

result = await complete_owned_receipt(client.inner, job, scope, connection)
```

This path never invokes business code. Missing receipts and conflicts require
the owner's explicit recovery policy; absence does not establish rollback or
exclusive ownership. Completion callbacks repeat the same saved JSON bytes and
token. Errors and cancellation keep confirmed receipt progress in `result` where
available. Preserve `EventConsumerCommitUnknownError` uncertainty and keep raw
database/business diagnostics out of public logs.

Unknown COMMIT closes the worker's exclusively leased connection. Use the pool's
discard path and acquire a fresh idle autocommit connection for reconciliation;
do not reuse the connection from the failed operation.

An explicitly persisted retry/dead-letter action is separate: use the SDK method
with the same original job, reason, known rollback, stopped work and exclusive
ownership. The example never chooses a negative action from an exception.

Caller cancellation waits for active SQL, transaction cleanup and renewal join.
Drain owned operations before closing connections, the HTTP client or event loop.
Configure bounded driver calls and cooperative handlers; cancellation is not a
hard timeout for an in-flight transaction. See [recovery and cancellation details](../../docs/event-acknowledgment-python-worker.md).

Local validation covers compilation/lint, package inclusion and static checks.
No tests/assertions were added or changed and no local tests were run. Hosted
database/callback/cancellation acceptance remains required before release.

## Durable callback mode

`durable_consumer.py` adds explicit version-2 `python-durable` records. Install
`event_acknowledgment_intent_schema` beside the receipt table before accepting
these jobs. Keep the same protected queue, authenticated ingress, exclusive
ownership, idle autocommit connection and bounded driver contract. Use a separate
ingress/queue configuration for new durable-mode jobs; never relabel saved
version-1 immediate records. Each processor rejects the other mode/version.

```python
from durable_consumer import (
    accept_authenticated_durable_delivery,
    process_owned_durable_delivery,
    save_owned_completion_intent,
    recover_one_saved_callback,
)

# Authenticated ingress; enqueue_protected must commit before returning 202.
response = await accept_authenticated_durable_delivery(
    client.inner, original_headers, original_body, scope, queue,
)

# Exclusively owned job; apply has the same cursor/cancel/original-bytes contract.
queued = await process_owned_durable_delivery(client.inner, job, scope, connection, apply)
# Its fenced job ack follows a confirmed atomic business/receipt/intent COMMIT.
# A separate, available recovery host now owns prompt callback delivery.

# Explicit receipt-only reconciliation after unknown COMMIT, on a fresh connection:
queued = await save_owned_completion_intent(client.inner, job, scope, fresh_connection)

# One independent recovery-host iteration, not a business-processing queue job:
status = await recover_one_saved_callback(client.inner, recovery_connection, scope, shutdown)
# None means no due unclaimed intent; inspect non-None state/last_code.
```

The phases above are alternatives chosen by the durable owner, not one sequential
pipeline. Persist an explicit receipt-only phase with the queue's fenced update
before using it. Missing receipts require an owner policy. Never select negative
acknowledgments or infer rollback from exceptions. If cancellation or job ack
fails after confirmed staging, retain/retry the original job; the receipt and
immutable intent prevent repeated committed business effects or reset claims.

Recovery sends only saved callbacks, never business code or platform renewals.
The host owns polling, concurrency, client lifetime and connection acquisition;
no loop starts on import. Use returned local status to distinguish confirmation,
supersession and scheduled uncertainty. Database/HTTP are not atomic together;
unknown settlement can repeat the same callback safely through server idempotency.
Keep intent tokens/results and backups protected and retain newest metadata across
the stale-job/restore horizon. See the [durable protocol and claim fences](../../docs/event-acknowledgment-python-intents.md).

## Persist a failure decision

After business work has stopped and its transaction is known to have rolled back,
an exclusive owner can explicitly select a retry or dead-letter action. The
example persists this choice in the protected queue before touching the intent
store. It never selects an action from an exception or an unknown COMMIT.

```python
from durable_consumer import persist_owned_failure_decision, save_owned_failure_intent

# Known rollback, stopped work, exclusive ownership, explicit owner policy:
await persist_owned_failure_decision(
    client.inner, job, scope, "retry", "dependency_unavailable",
)
# Or "dead-letter", "invalid_business_input".

# Resume here with the persisted/reloaded owned failure record after a restart:
status = await save_owned_failure_intent(client.inner, job, scope, connection)
# Its fenced job ack follows a confirmed negative-intent COMMIT.
# The independent recovery host sends the stored callback.
```

The additional adapter method `job.save_failure_decision(replacement)` must:

- Atomically persist the protected replacement under current ownership/fence.
- Preserve the original job ID, owner, headers and original body unchanged.
- Transition version-2 `python-durable` to version-3 `python-durable-failure`
  containing exactly one immutable `failure_decision` action/reason pair. Repeat
  identical transitions succeed; different decisions conflict.
- Refresh `job.record` with a detached committed snapshot before returning.
- Reject stale ownership and honor bounded persistence/cleanup deadlines.

The helper validates the original delivery and the reason code before persistence,
checks the refreshed record, and sends no callback or intent SQL in this phase.
If persistence fails, is cancelled, has an unknown outcome or leaves a stale view,
retire that handle and reload the protected record under ownership. Never assume
the phase stayed unchanged or reconstruct the choice from an error. A persisted
failure record must go only to `save_owned_failure_intent`; it reads the saved
choice and invokes no business handler. The version-2 business/receipt processors
reject version-3 failure records and any record carrying a failure decision.
Existing immediate records and ordinary version-2 completion jobs retain their
formats and behavior.

Failure-decision persistence alone does not permit queue deletion. Ack only after
negative-intent COMMIT, with the adapter's independent ownership fence. Unknown
intent COMMIT closes the connection; acquire a fresh idle autocommit connection
and repeat the same persisted decision. A committed receipt blocks failure staging
and needs explicit receipt-only owner recovery; never erase/change the saved
decision or force a negative callback. Missing receipts alone do not prove rollback.

After confirmed staging, failed queue ack or a restart can repeat the same save
without resetting its recovery claim, counters or due time. Callback delivery
uses the existing recovery host and rechecks receipts. No schema change, platform
endpoint, automatic polling or external exactly-once guarantee is added. See the
[failure-intent contract](../../docs/event-acknowledgment-python-intents.md#persist-explicit-failure-decisions).

## Observe recovery health

Run a single observation explicitly from the monitoring host:

```python
from dataclasses import asdict
from durable_consumer import read_acknowledgment_recovery_health

# Separately leased idle autocommit psycopg async connection; no platform client.
health = await read_acknowledgment_recovery_health(monitoring_connection, scope, shutdown)
record_recovery_health(asdict(health))
```

The helper samples up to 1000 unfinished intents plus one truncation-evidence row,
using a five-minute stall threshold and one database snapshot/clock. Always export
`coverage`, `complete` and `truncated` with counts/ages. Stalled work excludes live
claims and future retries. Incomplete coverage cannot establish health when the
sample reports zero stalled rows. The frozen summary contains owner identifiers
and aggregate metadata, with no invocation IDs, tokens, results or original bodies.

No callback, claim, business operation or polling loop starts. The application
owns polling, alert policy, connection acquisition and driver/statement timeouts.
Native/event cancellation joins active SQL/cursor cleanup before returning. A
driver/cursor failure closes the leased connection; discard it through the pool.
Do not share monitoring connections with active processing or recovery. See the
[Python summary contract](../../docs/event-acknowledgment-intent-health.md#python-summary).

## Watch sustained recovery conditions

Start the watch explicitly from an available monitoring host:

```python
from dataclasses import asdict
from durable_consumer import watch_acknowledgment_recovery

def connection_scope():
    # A new exclusive idle autocommit lease per read, with bounded acquisition.
    return monitoring_pool.connection(timeout=3)

async def notify(alert, cancel):
    cancel.check()
    await record_recovery_alert_locally(asdict(alert))
    cancel.check()

await watch_acknowledgment_recovery(connection_scope, scope, shutdown, notify)
```

The example enables due backlog at 100 and stalled recovery at 1, with 30 seconds
of sustained proof. It uses summary defaults, five-second sequential polling and
a five-second cooperative read budget including acquisition/cleanup. Configure
the pool to discard failed/closed connections and bound driver/statement/cleanup
calls. The factory returns an async context manager, not a connection or coroutine.
No next poll begins until the preceding scope and notifications finish.

Notifications are local application callbacks, not a configured transport. They
receive frozen metadata and a read-only cancellation view; persist/deduplicate
them through the application's own protocol when needed. A callback error stops
the watch with `EventAcknowledgmentIntentWatchCallbackError.alert` and no raw cause;
the notification outcome is unconfirmed. Slow callbacks can cause lost continuity.

Only fresh complete observations sustain firing or resolution. Failed/incomplete/
stale/out-of-order/gapped evidence produces unknown/lost-confirmation alerts,
never a false resolution. Repeated unchanged unknown reasons are suppressed.
State resets on restart. Native/event cancellation joins active reads, cleanup
and callbacks, then raises `EventAcknowledgmentWorkerCancelled` without resolving
alerts. Drain the watch before closing the pool/loop. No schema installation,
callback recovery, business work or monitoring loop starts on import. See the
[Python watch protocol](../../docs/event-acknowledgment-intent-health.md#python-watch).

## Persist recovery alert checkpoints

`watch_acknowledgment_recovery_with_checkpoint` adds an explicit fenced store
adapter and idempotent notification callback to the same monitoring setup:

```python
from faas_sdk import EventAcknowledgmentIntentCheckpointWatchCancelled
from durable_consumer import watch_acknowledgment_recovery_with_checkpoint

try:
    await watch_acknowledgment_recovery_with_checkpoint(
        connection_scope, scope, shutdown, store, notify_idempotently,
    )
except EventAcknowledgmentIntentCheckpointWatchCancelled as stopped:
    # Independent bounded shutdown context; the store still rejects stale ownership.
    await store.save_owned_on_shutdown(stopped.result["checkpoint"])
    raise
```

The source helper loads the owned checkpoint, applies optional exact
acknowledgments, and commits every `snapshot["checkpoint"]` **before** attempting
any pending notification. It records a delivery acknowledgment only after the
destination confirms that exact incident/sequence pair. Pending history replays
at startup before any health query, even if the health database is unavailable.

The additional adapter contract is:

- `load_owned(stop_event)` reads current state under the monitoring writer fence.
- `save_owned(checkpoint, cancel)` atomically commits the complete snapshot while
  rejecting stale writers; detached snapshots must be stored unchanged.
- `list_acknowledgments(checkpoint, cancel)` returns nondestructive durable exact
  pairs still pending or retained as receipts. Filter old ledger rows after their
  checkpoint receipts are safely committed.
- `record_acknowledgment(pair, cancel)` durably records confirmed delivery; retries
  of the same pair are idempotent.
- `save_owned_on_shutdown(checkpoint)` uses an independent bounded shutdown
  context and the same ownership fence. Keep ownership until it completes; lost
  owners cannot overwrite a successor.

Keep one serialized writer, including offline acknowledgment updates. Configure
bounded cooperative storage/notification work. `notify(notification, cancel)` must
deduplicate by owner plus incident ID and sequence, and resolve only after actual
destination confirmation. A successful notification with a lost acknowledgment
write repeats on replay; that is expected at-least-once delivery.

Callbacks receive detached metadata. A callback failure stops with
`EventAcknowledgmentIntentCheckpointWatchCallbackError.phase` and `.result`.
Reconcile its checkpoint against current fenced storage before retry; a failed
save may already have committed. Do not blindly overwrite state or infer rollback.
Cancellation retains pending notifications/unresolved IDs and requires fresh proof
on resume, without resolving or acknowledging them automatically. Shutdown save
failure is also an application persistence outcome requiring reconciliation.

Checkpoint kind/version are Python-specific and reject Node/Go/deadline formats.
The bounded outbox reserves closing capacity for unresolved incidents and blocks
new openings when full. No alert table, file, transport or polling service is
installed. Ordinary monitoring and callback recovery behavior remain unchanged.
See [validation, exact acknowledgments and resume semantics](../../docs/event-acknowledgment-python-checkpoints.md).

## Complete with a processing lease

`processing_consumer.py` adds explicit version-4 `python-durable-processing` jobs.
Configure separate authenticated ingress with
`accept_authenticated_processing_delivery` and explicitly install the receipt,
processing and intent schemas. Preserve the same protected queue, exclusive fenced
ownership and bounded idle autocommit connection contract. Earlier immediate,
durable and failure-decision records are rejected; never relabel them as leased.

```python
from processing_consumer import (
    claim_owned_processing_delivery,
    complete_owned_processing_delivery,
    save_owned_processing_completion_intent,
)

async with application_pool.connection() as connection:
    lease = await claim_owned_processing_delivery(
        client.inner, job, scope, connection, 30_000,
    )
# The host may now perform bounded side-effect-free preparation when
# lease.completed is false. It owns local renewal and the platform lease/budget.
# Stop and join local renewal before the short completion below.

async def apply(cursor, cancel, original_body):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    cancel.check()
    return b'{"applied":true}'

async with application_pool.connection() as connection:
    queued = await complete_owned_processing_delivery(
        client.inner, job, scope, connection, lease, apply,
    )
# The helper fences job.ack after confirmed writes/receipt/claim-removal/intent COMMIT.
```

Claiming/renewing the local lease does not renew the platform acknowledgment lease.
Neither helper coordinates preparation or keeps a VM awake. If the attempt expires
during preparation, new business work must fail platform confirmation and wait for
valid redelivery. Serialized local renewal uses `lease.renew` with separately leased
connections; stop on any failure/uncertainty. The per-object guard is not queue
ownership, and an expired process can remain physically running.

The read-only `lease.completed` snapshot lets the host skip preparation. Completion
still validates/replays the receipt, skips business code and stages the immutable
saved callback. Incomplete claims fence the final SQL writes, not external effects;
those require an outbox/idempotency. Failed preparation leaves the claim to expire
without automatically choosing a negative action.

After unknown COMMIT, retain the job, discard the closed connection and explicitly
select receipt-only recovery with `save_owned_processing_completion_intent` on a
fresh connection. It never claims/renews or prepares. A missing receipt needs the
owner's explicit policy; it proves neither rollback nor ownership. Repeating normal
completion is also safe only under the original input and a still-valid local
fence when no receipt exists. Recovery sends saved callbacks without business work.
See [the full Python lease contract](../../docs/event-consumer-processing-python.md).

## Coordinate preparation and both leases

`prepared_consumer.py` adds explicit version-5 `python-prepared-durable` jobs.
Use `accept_authenticated_prepared_delivery` on separately configured authenticated
ingress. Install the same receipt, processing and intent schemas. Keep protected
original inputs, exclusive fenced job ownership and bounded idle autocommit
connection scopes. Never relabel earlier immediate, durable, failure or manually
leased records; each processor rejects other modes and failure decisions.

```python
from faas_sdk import EventAcknowledgmentPreparedOptions
from prepared_consumer import process_owned_prepared_delivery

def connection_scope():
    # Synchronous factory; new exclusive idle autocommit scope on every call.
    return application_pool.connection(timeout=3)

async def prepare(original_body, cancel):
    cancel.check()
    prepared = await read_and_validate_without_effects(original_body, cancel)
    cancel.check()
    return prepared

async def apply(cursor, cancel, original_body, prepared):
    cancel.check()
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (prepared.invoice_id,))
    cancel.check()
    return b'{"applied":true}'

queued = await process_owned_prepared_delivery(
    client.inner, job, scope, connection_scope, prepare, apply,
    EventAcknowledgmentPreparedOptions(preparation_timeout_ms=60_000),
)
# The helper independently fences job.ack after confirmed atomic local completion.
```

The SDK first stages any saved receipt without preparation, processing-table access
or platform renewal. Otherwise it confirms the exact platform attempt, claims the
local row and renews both independently during preparation. Local renewal uses
separate short connection scopes; no business transaction stays open. It stops
and joins local renewal, refreshes once and checks its budget before the final
fenced writes/receipt/claim-removal/intent transaction. Platform renewal continues
through that transaction. The recovery host sends only saved callbacks afterward.

Preparation must be side-effect-free, cooperative and free of detached work.
Prepared data is passed in memory and never persisted/logged by the SDK. Keep the
final handler short and SQL-only; external effects need an outbox/idempotency.
Bound pool, driver and cleanup calls, and keep the host/event loop available.
Timeouts signal cancellation and join work rather than forcibly cancelling SQL.

Unknown claim/renewal/final COMMIT remains unknown even after cancellation or a
different callback failure. Retain the job, discard closed connections and apply
the owner's explicit reconciliation policy. For a possibly committed final
transaction, `save_owned_prepared_completion_intent` on a fresh connection never
prepares, claims or invokes business code. Missing receipts establish neither
rollback nor ownership. No automatic retry/dead-letter or claim release is chosen.
Confirmed intent commit remains confirmed after late cancellation/renewal/cleanup
failure; failed job ack can safely replay the receipt/intent handoff. See the
[coordinated Python lifecycle](../../docs/event-acknowledgment-python-preparation.md).

## Inspect a processing job

The example provides `processing_consumer.inspect_owned_processing_delivery` for
version-4 manually leased jobs and
`prepared_consumer.inspect_owned_prepared_delivery` for version-5 coordinated jobs.
Each validates its existing protected record and trusted owner scope, constructs
the original consumer request and performs one read-only inspection. No helper
claims, renews, prepares, sends callbacks, stages intents or acknowledges the job.

```python
from dataclasses import asdict
from prepared_consumer import inspect_owned_prepared_delivery

async with diagnostic_pool.connection() as connection:
    evidence = await inspect_owned_prepared_delivery(
        client.inner, job, scope, connection,
    )
record_processing_evidence(asdict(evidence))
```

Supply a separately leased idle autocommit async connection with bounded driver
calls. Use a read-authorized role and authorize diagnostic access to protected
original inputs. The public SDK inspector itself needs no platform client or
acknowledgment token; the example retains its normal protected-job validation.
Each helper rejects other job versions/modes and failure decisions. It does not
change the record or silently select an execution phase.

Matching receipt metadata is not saved-result validation. Reconcile through the
appropriate existing receipt-only helper before callback handoff. Active/expired/
absent/conflicting snapshots are operator evidence, never admission or proof of
rollback/stopped work. Keep jobs and immutable evidence, use live claim/complete
fences and persist recovery decisions under current ownership. No automatic retry,
dead-letter, release or repair is selected. Query failures discard the connection;
unavailable evidence is never an `absent` observation. See the
[snapshot and cancellation contract](../../docs/event-consumer-processing-inspection.md#python-inspection).

## Observe coordinated preparation

`prepared_options_with_diagnostics` configures the existing version-5 processor
with a synchronous observer and application-owned bounded `asyncio.Queue`:

```python
import asyncio
from prepared_consumer import prepared_options_with_diagnostics, process_owned_prepared_delivery

diagnostics = asyncio.Queue(maxsize=256)  # Capacity chosen by the host.
options = prepared_options_with_diagnostics(60_000, diagnostics)
queued = await process_owned_prepared_delivery(
    client.inner, job, scope, connection_scope, prepare, apply, options,
)
```

The helper rejects unbounded queues and forwards frozen primitive metadata using
`put_nowait(asdict(event))`. Queue-full and other observer exceptions are isolated;
events may drop. The host owns a separate bounded drain to its diagnostic backend,
including shutdown and availability. No drain, transport or worker loop starts on
import. The SDK schedules no async observer work and never waits for diagnostics
before acknowledging an already committed job.

Successful heartbeats emit nothing. At most nine events describe the operation's
start, claim/preparation, first failure per lease, confirmed/unknown commit and
shutdown. No event includes owner/event IDs, original bytes, tokens, prepared data,
saved results or raw exceptions. Callback duration and total elapsed time use a
monotonic clock and do not establish lease validity.

The same protected job version, phases and ownership fence apply. Diagnostics
never select retry/dead-letter, release claims or prove rollback/completion.
Keep observers passive and nonblocking; blocking callbacks can consume budgets
and stall timers even though exceptions are isolated. Callback recovery remains
independent of telemetry. See the [Python event and isolation contract](../../docs/event-acknowledgment-python-telemetry.md).

## Collect preparation metrics

Use one long-lived process-local collector in place of the diagnostic queue
observer. The existing version-5 processing helper and protected records stay
the same:

```python
from faas_sdk import EventAcknowledgmentPreparationMetrics
from prepared_consumer import prepared_options_with_metrics, process_owned_prepared_delivery

metrics = EventAcknowledgmentPreparationMetrics()
options = prepared_options_with_metrics(60_000, metrics)

queued = await process_owned_prepared_delivery(
    client.inner, job, scope, connection_scope, prepare, apply, options,
)

# Later, in the host's scrape/export path; never export inside the observer.
snapshot = metrics.snapshot()
```

The helper binds synchronous `metrics.record` directly. No exporter, listener,
drain or background task starts. Calls on the same asyncio event-loop thread
serialize naturally; the host must serialize all record/snapshot accesses from
other threads. Snapshots are detached mutable JSON-safe dictionaries/lists, so
changing one cannot change the collector or an earlier snapshot.

Counters use only fixed labels and histograms use fixed inclusive cumulative
millisecond buckets, including a final `None` bound for positive infinity. Publish
absolute cumulative totals, handle restart/reset detection, and check sticky
saturation before deriving rates/averages. Duplicate observations count again;
missing shutdowns or crashes prevent active-work or deduplication inferences.
Metrics select no recovery and prove no acknowledgment. See
[counting, validation and export details](../../docs/event-acknowledgment-python-metrics.md).

## Expose preparation metrics to Prometheus

`metrics.py` exports `create_preparation_metrics_app(metrics)`, an explicit ASGI
application factory. Supply the same long-lived collector passed to `on_event`.
Keep scraping on the processing event-loop thread, in one process. For example,
with an application-supplied Uvicorn installation and host lifecycle:

```python
import uvicorn  # Supplied by the application, outside the SDK's dependencies.
from metrics import create_preparation_metrics_app

scrape_app = create_preparation_metrics_app(metrics)
scrape_server = uvicorn.Server(uvicorn.Config(
    scrape_app, host="127.0.0.1", port=9464, workers=1,
    lifespan="off", access_log=False,
))
# The host runs await scrape_server.serve() alongside its owned workers on the
# same event loop. On shutdown, set scrape_server.should_exit = True and await
# server completion, then finish draining workers before closing their resources.
```

Importing or creating the app opens no listener. The host owns server startup,
listener failures, concurrent host tasks and shutdown. Do not launch a separate
Uvicorn process or worker thread with its own collector; it would observe a
different stream or require external access serialization. The app handles only
HTTP; disable lifespan as shown and keep delivery ingress separate.

Only `GET /metrics` without a query is served. Other paths/queries return 404;
other methods return 405 with `Allow: GET`. The full snapshot/body is generated
synchronously before the first response await. Validation errors return generic
503 without raw diagnostics; host cancellation and unexpected host failures
propagate. All responses use `Cache-Control: no-store`. Successful responses use
the exported Prometheus text 0.0.4 content type.

Configure a same-host scrape target at `127.0.0.1:9464`. Remote scrapes use the
host's authenticated proxy or protected transport; no authentication layer is
provided by this example. Reuse the collector across scrapes, publish absolute
cumulative totals, preserve process/reset boundaries and export outside the
observer. No monitoring deployment or metrics library is installed. See
[families, seconds, saturation and validation](../../docs/event-acknowledgment-python-prometheus.md).
