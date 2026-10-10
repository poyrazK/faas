# Transactional event consumers

The Go SDK can commit an event-consumer receipt and application PostgreSQL writes
in one transaction. If the application commits and loses its HTTP response, a
retry returns the saved JSON result without executing the business callback again.
Concurrent duplicates serialize before reading the receipt.

This first integration covers application subscriptions to published internal
CloudEvents. It does not cover direct application inbox messages, workflow steps,
object-notification adapters, or webhook receivers. Node and Python transaction
helpers are not yet available.

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

`EventConsumerRequestFromHTTP` reads the four event-context headers and takes
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
inbox-schema generator packages it as `sdk/go/event_consumer_schema.sql`.

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
