# Transactional event consumers

The Go, Node and Python SDKs can commit an event-consumer receipt and application
PostgreSQL writes in one transaction. If the application commits and loses its HTTP response, a
retry returns the saved JSON result without executing the business callback again.
Concurrent duplicates serialize before reading the receipt.

This integration covers application subscriptions to published internal
CloudEvents. It does not cover direct application inbox messages, workflow steps,
object-notification adapters, or webhook receivers.

## Identity and delivery context

New durable subscription deliveries carry `X-Gregale-Event-Acceptance-Id`, the
positive decimal ID of the retained event acceptance, alongside the existing
source, event ID and subscription ID headers. The original invocation's headers
survive execution retries and replay children. Subscription backfill uses the
original acceptance ID with its selected subscription. Legacy delivery rows and
non-durable routing without an acceptance ID cannot use this helper; it rejects
them rather than guessing from an invocation ID or occurrence timestamp.

Deduplication is scoped to account, application, subscription and acceptance ID.
Two consumers can process the same event independently. A new acceptance after
receipt pruning has a different ID, even if the producer reuses the source/event
ID. An execution replay of an existing acceptance returns its saved application
result; it does not intentionally repeat committed business effects. Use a new
business event when another mutation is intended.

The Go `EventConsumerRequestFromHTTP` factory reads the four event-context headers and takes
account/application IDs from owner configuration. Authenticate and authorize the
handler before calling it. Direct public Gregale HTTP traffic strips
`X-Gregale-Event-*` headers; the constructor itself is not authentication for
servers outside that boundary. Other authorized producers with invocation access
remain subject to application authorization. Do not trust arbitrary forwarded
headers or use receipt identity to grant business privileges.

The constructor requires nonzero UUID scope/consumer IDs, a canonical positive
int64 acceptance ID, matching CloudEvents source/ID, version 1.0, type and data,
and original UTF-8 JSON body bytes up to 1 MiB. It rejects duplicate event headers.
`NewEventConsumerRequest` supports adapters that already have authenticated
identity. Requests retain a private copy of the body.

## Install and consume

Install `faas.EventConsumerReceiptSchema` explicitly in the application's
PostgreSQL database as its owner. The wrapper does not install tables, delete
receipts, or start a background worker. Use a `database/sql` PostgreSQL driver.

After authenticating an internal delivery and reading its bounded original body:

```go
input, err := faas.EventConsumerRequestFromHTTP(r, body, accountID, appID)
if err != nil {
    // Reject invalid context before making any business writes.
    return err
}
result, err := faas.WithEventConsumerTransaction(ctx, db, input,
    func(tx faas.EventConsumerSQLTransaction) (json.RawMessage, error) {
        // Authorize and validate the business input before this mutation.
        _, err := tx.ExecContext(ctx,
            "UPDATE invoices SET paid = true WHERE id = $1", invoiceID)
        if err != nil {
            return nil, err
        }
        return json.RawMessage(`{"applied":true}`), nil
    })
if err != nil {
    // Do not acknowledge success. An uncertain commit must be reconciled
    // by retrying the same acceptance, consumer and original body.
    return err
}
// Only now acknowledge: send result.Body unchanged as application/json.
// result.Replayed reports whether a committed receipt supplied this result.
```

The callback uses only the supplied transaction. It must not commit, roll back,
retain the transaction, or make external API calls. Validation and callback errors
roll back both the receipt and business writes. Context cancellation bounds lock
waiting and database calls. A commit acknowledgement error wraps
`ErrEventConsumerCommitUnknown`; do not automatically rerun the callback under a
new identity. Retrying the same input checks for the committed receipt first.

Results must be valid UTF-8 JSON, between one byte and 1 MiB, and representable as
PostgreSQL JSONB. Store exact JSON text so saved response bytes remain unchanged.
Changed original body bytes under the same identity return
`ErrEventConsumerReceiptConflict`, including formatting changes. Attempts and
volatile HTTP headers do not affect the input digest. Database read errors are
propagated; they never trigger the callback as though the receipt were missing.

## Shared PostgreSQL contract

The canonical application-owned DDL is `pkg/eventinbox/schema.sql`; the existing
inbox-schema generator packages identical DDL and limits for all three SDKs.

The identity digest is SHA-256 of the following UTF-8 bytes, with no final newline:

```text
gregale.event-consumer.v1\n<lowercase account UUID>\n<lowercase app UUID>\n<lowercase subscription UUID>\n<canonical decimal acceptance ID>
```

The input digest is SHA-256 of `gregale.event-consumer-input.v1\n` followed by the
exact original body bytes. Here `\n` denotes one LF byte. UUIDs and decimal IDs
cannot contain LF, so identity framing is unambiguous.

Each helper owns a fresh READ COMMITTED transaction. It acquires
`pg_advisory_xact_lock(hashtextextended($1, 0))` with the text
`gregale.event-consumer-inbox.v1:` followed by the lowercase hexadecimal identity
digest. The receipt SELECT is a separate statement after the lock, so a waiting
transaction observes the previous transaction's committed receipt. A hash
collision only serializes unrelated transactions; full stored identity, source,
event ID and input digest are checked before replaying a result. A missing receipt
permits the callback, receipt insertion, and one commit. No incomplete receipt or
processing lease is committed. Process death releases transaction locks.

## Retention and limits

Keep receipts for as long as executions, historical backfills or operator replays
can return. Gregale's event-receipt retention does not prune application receipts.
Deleting an application receipt permits its business writes to execute again;
choose cleanup only after the relevant replay paths have ended. There is no
automatic TTL or cleanup API in this integration.

The guarantee covers one committed set of writes and a receipt in this PostgreSQL
transaction. Callback code can execute again after a rollback. Use business
constraints and row locks for invariants beyond READ COMMITTED isolation. External
APIs require their own idempotency keys. Gregale completion and application
PostgreSQL remain separate transactions; delivery remains at least once.

## Node handler

Install `eventConsumerReceiptSchema` explicitly as the database owner. Supply a
`pg.Pool` to the wrapper; it leases and releases a dedicated connection. Acceptance
IDs are decimal **strings**, including in `newEventConsumerRequest`, so values
above JavaScript's safe-integer range remain exact. After authentication and
business authorization, with the original body captured as a `Buffer`:

```js
import { eventConsumerRequestFromHeaders, withEventConsumerTransaction }
  from '@gregale/sdk-node';

const input = eventConsumerRequestFromHeaders(req.headers, rawBody, accountId, appId);
const result = await withEventConsumerTransaction(pool, input, async tx => {
  await tx.query('UPDATE invoices SET paid=true WHERE id=$1', [invoiceId]);
  return JSON.stringify({ applied: true });
});
res.type('application/json').send(result.body);
```

The callback returns JSON **text**, not an arbitrary object. The wrapper validates
it and stores exact text. It exposes queries without connection release; do not
issue transaction-control SQL or make external effects. Connection/statement
timeouts remain configured on the pg pool. `EventConsumerConflictError` rejects
changed content. `EventConsumerCommitUnknownError` means retry the same immutable
request; connections with an uncertain commit or failed rollback are discarded.

## Python synchronous handler

Install `event_consumer_receipt_schema` explicitly. Lease an idle psycopg connection
with `autocommit=True` exclusively for the whole wrapper call. Do not share it
between threads/tasks, enter an outer transaction, or change transaction settings
inside the callback. After authenticating and authorizing the input:

```python
import json
from faas_sdk import event_consumer_request_from_headers, with_event_consumer_transaction

input = event_consumer_request_from_headers(headers, raw_body, account_id, app_id)

def apply(cursor):
    cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    return json.dumps({"applied": True}).encode("utf-8")

with pool.connection() as connection:  # pool configured with autocommit=True
    result = with_event_consumer_transaction(connection, input, apply)
# Send result.body unchanged as application/json only after success.
```

## Python asynchronous handler

```python
from faas_sdk import awith_event_consumer_transaction

async def apply(cursor):
    await cursor.execute("UPDATE invoices SET paid=true WHERE id=%s", (invoice_id,))
    return b'{"applied":true}'

async with pool.connection() as connection:  # AsyncConnectionPool, autocommit=True
    result = await awith_event_consumer_transaction(connection, input, apply)
# Only now acknowledge with result.body unchanged as application/json.
```

Python callbacks return UTF-8 JSON bytes or text. Acceptance IDs are exact positive
int64 Python integers; header factories require canonical decimal strings. Internal
receipt cursors use tuple rows without changing the business cursor's configured
row factory. Configure PostgreSQL statement/lock timeouts for bounded waits.
Errors before completion roll back; cancellation or an exception during commit
can raise `EventConsumerCommitUnknownError`. Retry the same identity and bytes.
`EventConsumerConflictError` prevents the callback from running for changed input.

All adapters use the same table, SHA-256 framing and advisory lock namespace. A
receipt written by one SDK can be read by another without repeating committed
writes, provided the original request bytes, scope and acceptance are preserved.

## Update and enqueue follow-up work atomically

Install both the consumer receipt schema and the existing
[Commit outbox schema](gregale-commit.md) in the **same application database**.
Configure a Commit source bound to the follow-up application and queue, and enable
its relay under the documented Commit deployment requirements. Inserting an
outbox row alone does not configure or enable delivery. Commit starts managed
Operations at that fixed destination; this is not publication to event subscribers.

Inside the consumer callback, update business rows and insert follow-up work using
only its supplied transaction. For example, after marking an invoice paid, enqueue
receipt generation and include its outbox ID in the saved response:

```go
// Inside WithEventConsumerTransaction's callback, after the business update:
followUpID, err := faas.InsertCommitEventInTransaction(ctx, tx,
    faas.CommitEventRequest{
        Type: "invoice.receipt.generate",
        Data: json.RawMessage(`{"invoice_id":"123"}`),
    })
if err != nil {
    return nil, err
}
return json.Marshal(map[string]string{"follow_up_id": followUpID})
```

The Go adapter accepts the restricted `EventConsumerSQLTransaction` without
exposing transaction lifecycle methods. Existing `InsertCommitEvent(ctx, *sql.Tx,
event)` callers remain compatible. Both helpers perform the same validation.

```js
import { insertCommitEvent } from '@gregale/sdk-node';

// Inside withEventConsumerTransaction's async callback, after the update:
const followUpId = await insertCommitEvent(tx, {
  type: 'invoice.receipt.generate', data: { invoice_id: '123' }
});
return JSON.stringify({ follow_up_id: followUpId });
```

```python
from faas_sdk import insert_commit_event, ainsert_commit_event

# Inside the synchronous callback, after the business update:
follow_up_id = insert_commit_event(
    cursor, "invoice.receipt.generate", {"invoice_id": "123"}
)
return json.dumps({"follow_up_id": follow_up_id})
```

```python
# Inside the asynchronous callback, after the awaited business update:
follow_up_id = await ainsert_commit_event(
    cursor, "invoice.receipt.generate", {"invoice_id": "123"}
)
return json.dumps({"follow_up_id": follow_up_id})
```

Propagate insertion errors to the transaction wrapper. A rollback leaves no
business update, follow-up row or consumer receipt. A committed input retry reads
the saved response without invoking the callback or inserting another follow-up.
If commit acknowledgement is uncertain, retry the same consumer input and original
body rather than enqueueing separately. Generated outbox IDs from rolled-back
attempts have no committed work; the committed ID remains in the saved response.
Explicit IDs must be stable UUIDs unique to each intended follow-up, including its
consumer scope when different subscriptions produce independent work. Duplicate
outbox IDs remain errors, not successful no-ops.

The relay can accept the follow-up after the transaction commits, even before the
consumer's HTTP response arrives. Consumer completion, Commit acceptance and the
follow-up Operation's completion are separate facts. A saved follow-up ID confirms
its atomic enqueue, not execution success. Keep application receipts across all
relevant replay paths; deleting one can allow another enqueue. Follow-up consumers
still need idempotent business effects, and external calls must not happen inside
this database callback.

To emit an event to independent subscribers instead of a fixed Operation target,
bind the outbox to a [version 3 event-publication source](gregale-commit.md#version-3-application-event-publication-adr-935).
The same callback inserts compose; the source destination determines whether
Gregale accepts a managed Operation or an application-event fanout. Install the
publication checkpoint upgrade explicitly and retain consumer receipts across
all relevant replay paths.

## Long-running preparation

The Go SDK offers [application-owned processing leases](event-consumer-processing.md)
for durable claims, renewal and fenced transactional completion before HTTP success.

## Deferred acknowledgment worker (Go)

[Go worker acknowledgments](event-acknowledgment-worker.md) connects these receipt
transactions and optional preparation leases to platform renew/complete/retry/
dead-letter callbacks, with receipt-only recovery after uncertain commits.
