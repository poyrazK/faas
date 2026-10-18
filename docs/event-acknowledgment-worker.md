# Go worker acknowledgments

For Node applications, see the [Node acknowledgment worker](event-acknowledgment-node-worker.md).

`EventAcknowledgmentWorker` connects the Go consumer receipt transaction to deferred
platform acknowledgments. It renews the platform lease while the transaction runs,
completes only after a confirmed receipt commit, and retries callbacks with unchanged
inputs. It neither installs tables nor creates a durable queue or background service.

Install `faas.EventConsumerReceiptSchema` in the application database first. Use an
owned durable worker that can reach apid, with appropriately scoped API credentials.
A goroutine started by an HTTP handler does not become durable when it returns 202,
and the application VM can park after the request ends.

## Accept and reconstruct

Authenticate and authorize the internal delivery route before parsing headers.
The factory validates receipt identity, original CloudEvent body, attempt/generation
metadata, a single canonical acknowledgment token and a lease of 1–300 seconds.
It is not an authentication mechanism. The API app slug and owner account/app UUIDs
come from trusted application configuration.

```go
worker, err := faas.EventAcknowledgmentWorkerFromHTTP(client, appSlug, r,
    originalBody, accountID, appID, faas.EventAcknowledgmentWorkerOptions{})
if err != nil {
    return err
}
```

Before returning 202, durably enqueue the original body and required delivery
headers, including the token, with exclusive job ownership and protected storage.
Keep event identity headers, invocation source/ID, attempt/replay generation, optional
lineage headers, and both acknowledgment headers. Do not normalize or reconstruct
the original body. The consumer's durable worker rebuilds an `http.Request` header
container from that protected record and calls the same factory after restart.
No original HTTP connection is required. Do not put tokens in logs, URLs or job
identifiers. Worker formatting and JSON serialization redact its capability; they
are deliberately not a persistence format. Treat custom HTTP transports as trusted:
they can observe outbound request bodies. Business errors remain application-owned.

## Process and complete

```go
result, err := worker.Run(ctx, db,
    func(workCtx context.Context, tx faas.EventConsumerSQLTransaction) (json.RawMessage, error) {
        // Validate/authorize business input, then use workCtx for every query.
        _, err := tx.ExecContext(workCtx,
            "UPDATE invoices SET paid = true WHERE id = $1", invoiceID)
        if err != nil {
            return nil, err
        }
        return json.RawMessage(`{"applied":true}`), nil
    })
```

Use only the supplied transaction; never commit/rollback it or perform external
effects in this callback. Use an outbox for other effects. A previously committed
receipt replays without calling business code, including when the platform already
completed the attempt and renewal would return 409. Otherwise the helper requires
a successful token-fenced renewal before starting business work. A 409 during the
202 handoff can be retried only while a scoped GET still says `delivering`; that
GET never authorizes starting work.

During processing, renewal runs every third of the advertised lease. Any renewal
failure cancels the work context: definite conflicts/missing claims report
`ErrEventAcknowledgmentLeaseLost`; transport, authorization or evidence failures
report `ErrEventAcknowledgmentUnconfirmed`. Cancellation is cooperative. Renewal
is not a distributed database fence and cannot stop arbitrary goroutines or undo
committed effects. Receipt serialization remains the business deduplication fence.
A late commit can still require receipt replay on a new platform attempt.

Renewal stops and joins before a terminal callback, so a completed response cannot
race a renewal into a false lease-loss signal. Completion uses the exact saved
receipt JSON. Callback uncertainty never causes business code to be retried within
the call. If the receipt commit is confirmed, completion may still be attempted
after a last renewal failure; the server's attempt/token fence decides its outcome.

Zero options select three callback attempts, a timeout of the smaller of five
seconds and one sixth of the lease, and backoff of the smaller of 100 milliseconds
and one twelfth of the lease. Overrides allow 1–10 attempts, a timeout between one
millisecond and that default maximum, and backoff between one millisecond and one
third of the lease. Retryable transport failures, 408/425/429 and 5xx use unchanged
inputs. Valid `Retry-After` is honored; a requested delay above one third of the lease
stops the call for durable-job rescheduling. Invalid retry hints also stop retries.
The client's transport may perform additional retries within each RPC deadline.
Set a parent context deadline to bound the whole operation. Live processing stops
on its first unconfirmed renewal instead of continuing through callback retries.
Remote error bodies are not included in helper callback errors.

## Recover uncertainty and choose failure dispositions

`ReceiptConfirmed` means the application receipt committed, even if the platform
callback failed. False does not prove rollback. Handle these cases explicitly:

| Result | Recovery |
| --- | --- |
| Receipt confirmed, callback unconfirmed | Call `CompleteReceipt(ctx, db)` later with the same protected job. It reads the receipt and never invokes business code. |
| `ErrEventConsumerCommitUnknown` | Reconcile with `CompleteReceipt`; do not infer rollback or send a negative acknowledgment. The receipt lookup serializes with outstanding consumer transactions. |
| Confirmed lease loss | Stop preparation. An old helper never adopts a replacement token. A redelivery rebuilds the helper and replays any committed business receipt. |
| `ErrEventConsumerReceiptMissing` | No matching receipt was observed by recovery. Do not treat that observation as an exclusive processing claim; follow the durable job's ownership/rollback policy. |
| Receipt conflict | Stop and investigate immutable input/identity mismatch; do not replace or delete the receipt. |
| Known rollback, stopped work, exclusive job ownership | Persist an explicit retry or dead-letter intent and its non-sensitive reason before calling the corresponding method. |

`worker.Retry(ctx, db, "dependency_unavailable")` and
`worker.DeadLetter(ctx, db, "invalid_business_input")` first check for a committed
receipt. If found, they return `ErrEventConsumerReceiptCommitted` and confirmed
receipt data without sending a negative callback; complete that receipt instead.
Otherwise they submit the existing fenced endpoints and retry identical inputs.
Persist the action/reason before the first call so a crash can repeat the same
intent. Do not change a negative disposition after an uncertain response. No
business error is automatically mapped to retry or dead-letter.

Copies of one worker share a guard and reject overlapping operations with
`ErrEventAcknowledgmentWorkerBusy`. Separate workers/processes still require
exclusive durable-job ownership. A receipt absence check is not a distributed lock
against another worker starting immediately afterward. Neither these helpers nor
lease expiry provide exactly-once external effects or new delivery ordering.

## Existing local preparation leases

`RunWithProcessingLease(ctx, db, lease, handler)` additionally uses
`EventConsumerProcessingLease.Complete`, after checking that the lease belongs to
the same immutable input. Install `EventConsumerProcessingSchema` for this mode.
Prepare and renew that application-owned lease using the existing helpers before
this final transaction. This worker renews only the platform lease: local Complete
holds the preparation row locked and checks expiry before commit. Keep the final
transaction within that local lease; do not run a competing local renewal loop
while Complete holds the row. An already committed receipt can replay after the
old local lease expires. Business receipts still require explicit application
retention and operational recovery policies.

## Persist callbacks across process death

[Durable Go acknowledgment intents](event-acknowledgment-intents.md) adds opt-in
application-owned storage, atomic receipt/intent commits, and leased recovery
without rerunning business code. Use that mode instead of immediate terminal
callbacks when the application wants database-backed intent recovery.
