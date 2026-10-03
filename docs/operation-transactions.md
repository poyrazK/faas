# Write business logic in a replayable PostgreSQL operation

The SDK transaction wrapper commits your business writes and the complete
managed-operation response in the same PostgreSQL transaction. If a handler loses
its response after commit, its next attempt returns the saved response and webhook
requests. Concurrent attempts share one database lock and one receipt.

Install the exported receipt schema explicitly as your database owner:

- Node: `operationReceiptSchema` from `@gregale/sdk-node`.
- Go: `faas.OperationReceiptSchema`.
- Python: `operation_receipt_schema` from `faas_sdk`.

The canonical DDL is [pkg/operationinbox/schema.sql](../pkg/operationinbox/schema.sql).
It creates `public.gregale_operation_inbox`. The application role needs SELECT
and INSERT on that table. The SDK performs no DDL or automatic cleanup. Retain
receipts while the original operation can still replay.

## Node

Capture raw bytes before Express parses the body. Use the header factory behind
Gregale ingress; it checks negotiated support and the platform-authored identity.
The callback contains ordinary business logic and returns delivery intent:

```js
import {
  operationRequestFromHeaders,
  withOperationTransaction,
} from '@gregale/sdk-node';

app.use(express.json({
  verify(req, _res, body) { req.rawBody = Buffer.from(body); },
}));

app.post('/orders/fulfill', async (req, res, next) => {
  try {
    const context = operationRequestFromHeaders(
      req.headers, req.method, req.originalUrl, req.rawBody,
    );
    const response = await withOperationTransaction(pool, context, async tx => {
      const order = await fulfillOrder(tx, context.platformTenantId, req.body.order_id);
      const webhookId = await configuredReceiver(tx, context.platformTenantId);
      return {
        result: {order_id: order.id, status: order.status},
        effects: [{
          name: 'customer-notification',
          webhook_id: webhookId,
          type: 'order.fulfilled',
          payload: {order_id: order.id, status: order.status},
        }],
      };
    });
    res.type('application/json').send(response.body);
  } catch (error) { next(error); }
});
```

`pool` is a pg-compatible connection pool that hands out idle, exclusively leased
connections. `fulfillOrder` and `configuredReceiver` use the supplied transaction.
They still enforce your business permissions and choose the registered receiver
from trusted configuration. They do not implement operation deduplication.
Omit `effects` or return `[]` when no webhook is needed. Send `response.body`
directly; `res.json(response.body)` would double-encode it. `response.replayed`
reports whether the SDK recovered an existing receipt.

## Go

Read the bounded original request bytes before parsing your business input. Build
the context with `faas.OperationRequestFromHTTP(r, body)`, then call:

```go
response, err := faas.WithOperationTransaction(ctx, db, operation,
    func(tx faas.OperationSQLTransaction) (faas.OperationOutcome, error) {
        order, err := fulfillOrder(ctx, tx, operation.PlatformTenantID, orderID)
        if err != nil { return faas.OperationOutcome{}, err }
        result, err := json.Marshal(order)
        return faas.OperationOutcome{Result: result}, err
    })
if err != nil { return err }
w.Header().Set("Content-Type", "application/json")
_, err = w.Write(response.Body)
```

Use `Effects: []faas.ManagedOperationEffect{...}` to include webhooks. A nil result
encodes JSON `null`; nil effects encode `[]`. The callback interface exposes
queries and execution, with the wrapper owning commit and rollback.

## Python

Use `operation_request_from_headers(headers, method, raw_target, raw_body)`.
Keep repeated header values rather than flattening them into an ordinary dict.
The target includes the original encoded path and query string. With an
exclusively leased psycopg connection configured with `autocommit=True`:

```python
from faas_sdk import with_operation_transaction

def business(cursor):
    order = fulfill_order(cursor, operation.platform_tenant_id, order_id)
    return {"result": order, "effects": []}

response = with_operation_transaction(connection, operation, business)
# Send response.body as application/json after the function returns.
```

For async psycopg, use `await awith_operation_transaction(connection, operation,
async_business)` with an async callback. The wrapper preserves your connection's
row factory for business queries. It rejects active transactions and non-autocommit
connections; do not share the leased connection with another request.

## Recovery contract

Every SDK verifies operation/account/app/customer identity and the fingerprint
of the exact method, target and body. A reused operation ID with another scope or
input fails before the callback runs. Generation and deployment changes do not
change receipt identity. The same receipt works across Node, Go and Python.

Callbacks may run again after rollback. Use only the supplied transaction for
business writes, let the wrapper control its lifecycle, and keep external side
effects out of it. The wrapper uses READ COMMITTED; enforce business invariants
with your database constraints and row locks. Headers that change business meaning
must be represented in the durable input. The factories depend on Gregale ingress
authoring their headers and do not authenticate arbitrary external HTTP servers.

An uncertain COMMIT returns `OperationCommitUnknownError` in Node/Python or wraps
`ErrOperationCommitUnknown` in Go. Retry the same operation identity; a committed
receipt will replay. The SDK never automatically reruns the callback after an error.
Other errors roll back the owned transaction before returning where connectivity
permits; a disconnected backend releases it through PostgreSQL recovery.

The saved response does not mean Gregale has accepted completion. A revoked
receiver or suspended tenant can still prevent platform completion after the
business transaction commits. Replay preserves the original receiver and payload.
See [managed webhook effects](managed-operation-effects.md) for signing, retries,
ownership fences, and delivery status. Intentionally starting another operation
ID creates new work; application-level business uniqueness still belongs in your
business logic.

## Acceptance

Against a disposable PostgreSQL cluster with CREATEDB permission, run:

```sh
DATABASE_URL='postgresql://...' \
GREGALE_OPERATION_PYTHON=/path/to/python-with-psycopg \
make test-operation-sdk
```

This requires the Node SDK development dependencies, Go, and Python with the SDK
and `sdk/commit-tests/requirements.txt` installed. It checks all three transaction
wrappers, actual HTTP process death before/after commit, and all nine cross-language
writer/reader pairs for both customer and account scope. It fails if the required
PostgreSQL tests are skipped. Native
managed-operation qualification remains a separate rollout gate under ADR-488.
CI runs this gate and the existing Commit SDK transaction acceptance with real
PostgreSQL in the `operation-sdk-acceptance` job.
