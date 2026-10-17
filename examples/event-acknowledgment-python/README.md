# Durable Python ingestion with immediate event acknowledgment

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
