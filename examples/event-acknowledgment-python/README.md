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
