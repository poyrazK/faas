# Consumer processing leases (Go SDK)

The Go SDK supports opt-in, application-owned PostgreSQL leases for long-running
preparation followed by a short business transaction. Each claim belongs to one
account, app, subscription and durable acceptance, independently of other consumers.
Original request bytes remain bound to that identity through expiry and takeover.

Install both `faas.EventConsumerReceiptSchema` and
`faas.EventConsumerProcessingSchema` explicitly in the application database.
The SDK does not install schemas, run workers or delete abandoned rows. Expired
rows can be taken over in place. Do not prune outstanding claims or receipts while
delivery retries and historical replays remain possible.

After authenticating delivery and constructing `EventConsumerRequest`:

```go
lease, err := faas.ClaimEventConsumerProcessing(ctx, db, input, 30*time.Second)
if err != nil {
    // Busy, database failure and invalid input are not successful delivery.
    return err
}
if !lease.Completed() {
    // Perform side-effect-free preparation. For longer preparation, renew well
    // before expiry. Serialize renewal with completion; stop if renewal fails.
    if err := lease.Renew(ctx, db, 30*time.Second); err != nil {
        return err
    }
}
result, err := lease.Complete(ctx, db,
    func(tx faas.EventConsumerSQLTransaction) (json.RawMessage, error) {
        // Apply business writes and optional Commit outbox inserts using tx.
        // No external effects and no transaction lifecycle calls here.
        return json.RawMessage(`{"processed":true}`), nil
    })
if err != nil {
    return err
}
// Only now return HTTP success with result.Body.
```

Durations must be positive whole milliseconds. Use a duration below the request
budget, a context deadline and a renewal margin appropriate to the application.
Database time decides expiry. An opaque random token fences each claim; neither
an invocation ID nor attempt metadata is a lease token. Expired tokens cannot
renew or complete an unfinished receipt. Takeover retains the request digest,
rejecting a different body under the same acceptance/consumer identity.

Completion locks the claim, validates expiry before the callback, and checks it
again when deleting the claim in the same transaction as business writes and the
receipt. Expiry during the callback rolls the transaction back. The final check
is the completion's linearization point; commit acknowledgment can arrive later.
A takeover waits for the completion transaction, then sees its receipt or takes
over after rollback. Keep callbacks short: renewal cannot proceed while completion
holds the claim row lock.

A successful completion deletes the lease and retains the ordinary deduplication
receipt. Repeating `Complete` on the same capability returns the saved result;
claiming a completed input returns `Completed() == true` without inserting a new
lease row. `Complete` still validates the saved receipt. Do not acknowledge based
on `Completed()` alone. Unknown completion commits must be retried with the same
identity and original bytes. Unknown claim commits require waiting for expiry and
claiming again; no token is returned on an uncertain claim commit.

Use this protocol consistently for a consumer. The existing unleased transaction
helper does not enforce processing claims, and mixing leased and unleased handlers
can duplicate preparation. Committed database effects remain deduplicated by the
shared receipt. External effects require their own idempotency protocol or an
outbox; a processing lease does not make them exactly once.

This integration keeps the HTTP request open until completion. A busy claim,
failed renewal or lost lease must produce a retryable failure, never an early 2xx.
Gregale's existing execution retry budget and dead-letter behavior remain in
force; lease expiry alone does not schedule another invocation. If a handler
crashes, its subsequent delivery can reclaim after expiry. There is no detached
202 acknowledgment mode or platform processing-health view in the original Go
integration. Python processing helpers remain separate.

For workers that durably continue beyond the HTTP request, use the separate
[platform deferred acknowledgment protocol](event-deferred-acknowledgments.md).
The application database lease and platform delivery lease protect different
boundaries; neither replaces the transactional business receipt.

The [Node processing-lease SDK](event-consumer-processing-node.md) now shares this
application-owned schema and adds immediate/durable acknowledgment-worker integration.

The [Python processing-lease SDK](event-consumer-processing-python.md) shares the
same schema, digests and completion fences, with async psycopg capabilities and
immediate/durable acknowledgment-worker composition.
