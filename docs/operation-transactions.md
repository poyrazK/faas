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

Customer Operations use a separate application-owned receipt table. Install
`customerOperationReceiptSchema` in Node, `faas.CustomerOperationReceiptSchema`
in Go, or `customer_operation_receipt_schema` in Python. Its canonical DDL is
[pkg/operationinbox/customer_schema.sql](../pkg/operationinbox/customer_schema.sql).
The application role needs SELECT and INSERT on the customer inbox table. Retain
those receipts while the original Customer Operation can still replay.

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

## Customer Operations HTTP adapter

Customer Operations opt in through their immutable definition or source manifest:

```yaml
operations:
  - name: export
    method: POST
    path: /exports
    owner: platform_tenant
    input_schema: schemas/input.json
    output_schema: schemas/output.json
    progress_stages: [generating]
    recovery: reconcile_on_unknown
    transaction_receipt: postgres_v1
```

Install the Customer Operations receipt schema explicitly. On the trusted
Gregale guest listener, use the Customer Operations factory and helper. In Node:

```ts
import {
  customerOperationReceiptRequestFromHeaders,
  withCustomerOperationReceiptTransaction,
} from '@gregale/sdk-node';

const request = customerOperationReceiptRequestFromHeaders(
  req.headers, req.method, req.originalUrl, req.rawBody,
);
const response = await withCustomerOperationReceiptTransaction(pool, request, async tx => {
  await tx.query('INSERT INTO exports(id, customer_id) VALUES ($1, $2)',
    [exportId, authorizedCustomerId]);
  return { export_id: exportId }; // ordinary JSON matching the output schema
});
res.type('application/json').send(response.body);
```

Go exposes `CustomerOperationRequestFromHTTP` and
`WithCustomerOperationTransaction`; its callback returns `json.RawMessage` and
an error. Python exposes `customer_operation_request_from_headers`,
`with_customer_operation_transaction`, and `awith_customer_operation_transaction`;
their callbacks return the ordinary JSON result. All return the existing
transaction result with `body`/`Body` and `replayed`/`Replayed`. Send the retained
body unchanged. The SDK preserves exact result bytes even when another language
created the receipt.

The factory requires explicitly negotiated support, a verified customer scope
and the ordinary HTTP claim context. It rejects managed-result and native context.
The platform authors a binding to the captured account, app, customer, scope,
definition revision, deployment and release. Receipt lookup checks that binding
and exact method, target and body; changing scope or input fails before callback.
The current claim proof is discarded by the factory and never saved. This factory
is not authentication on arbitrary external HTTP servers; the application still
owns business authorization.

Business writes and the saved result commit together in the customer database.
If the handler dies after COMMIT, Gregale retains `requires_reconciliation` until
an evidenced account recovery approves a new execution. That execution checks the
receipt first and returns a saved result without calling the business callback.
If no receipt exists, the approved callback can run. The SDK never automatically
retries work or an uncertain COMMIT. Keep receipts for as long as the captured
operation can be recovered; deleting one permits new callback execution.

This adapter supports only ordinary HTTP handlers and `reconcile_on_unknown`.
Keep external API calls, file uploads and progress reports outside the database
transaction; their effects need their own reconciliation evidence. Named managed
effects are unsupported. The internal receipt envelope is never sent as the
Customer Operations response. Gregale validates the plain result against the
captured output schema and fences stale or late completion. A saved database
result alone does not prove platform completion. The existing completion delivery
is queued once by platform completion and retried independently of business work.
Starting a new logical operation remains new work; enforce business uniqueness
with application constraints.

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

For Customer Operations, `make test-customer-operation-sdk` includes the SDK gate
below and requires memory/PostgreSQL control-plane acceptance with a separate
customer database. It kills the handler after COMMIT, approves recovery, replays
without callback, rejects old completion, and verifies one business write and one
logical completion delivery while the webhook retries. CI runs this gate.

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
managed-operation qualification remains a separate rollout gate under ADR-585.
CI runs this gate and the existing Commit SDK transaction acceptance with real
PostgreSQL in the `operation-sdk-acceptance` job.

On the native x86_64 KVM acceptance host, run the guest-runtime recovery gate
with:

```sh
DATABASE_URL='postgresql://...' \
FAAS_TEST_KERNEL=/path/to/vmlinux \
FAAS_BUILDER_BASE_PATH=/path/to/builder-base.ext4 \
make test-managed-operation-native
```

The `e2e-native` workflow exposes the same acceptance as the
`managed-operation-only` dispatch lane.

This deploys a source app into Firecracker, kills its handler after a local
recovery receipt is saved but before the response arrives, retries the same
workflow step, and checks generation 2, the stable operation ID, and one signed
`operation.effect` delivery. The fixture's receipt is local to the guest and
does not claim a PostgreSQL write; the SDK gate above supplies real PostgreSQL
commit and process-death evidence. ADR-585 promotion remains pending until this
native gate passes on the acceptance host.
