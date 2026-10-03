# Return a business result and let Gregale deliver its webhook

A managed HTTP operation can complete its result and queue named webhook effects
in one Gregale transaction. This works for managed app request operations,
including the operations accepted by Gregale Commit and account-scoped managed
workflow steps. The dispatcher handles signing, retries, delivery history, and
dead letters after completion.

Register a receiver with an explicit `operation.effect` subscription. For a
customer-scoped operation, create it under
`POST /v1/platform-tenants/{tenant_id}/webhooks` with an `event_filter` containing
`operation.effect`. The customer must belong to your account and have an active
surface linked to the operation's app. For an account-scoped operation, register
an app receiver under `POST /v1/apps/{slug}/webhooks` with that same explicit
filter. A workflow effect can target only a receiver owned by that workflow's
app; tenant receivers are not supported for workflow steps. An empty app filter
does not authorize operation effects. Store the receiver's UUID in your
application's configuration.

Your handler first checks `X-Gregale-Operation-Result-Version: 1`. If it is absent,
return a failure before doing business work. Both the scheduler and internal
gateway must support this protocol. Gregale also provides the trusted
`X-Gregale-Operation-Id` and `X-Gregale-Operation-Generation` headers.

Install the SDK's customer receipt schema explicitly before using the transaction
wrapper. Capture raw request bytes before Express parses JSON, as shown in the
[transactional handler guide](operation-transactions.md). The wrapper stores the
business writes, result, and webhook intent together:

```js
import { operationRequestFromHeaders, withOperationTransaction } from '@gregale/sdk-node';

app.post('/orders/fulfill', async (req, res, next) => {
  try {
    const operation = operationRequestFromHeaders(
      req.headers, req.method, req.originalUrl, req.rawBody,
    );
    const response = await withOperationTransaction(pool, operation, async tx => {
      const order = await fulfillOrder(tx, operation.platformTenantId, req.body.order_id);
      const webhookId = await configuredReceiver(tx, operation.platformTenantId);
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

`fulfillOrder` and `configuredReceiver` use only the supplied transaction and
enforce business authorization. The wrapper handles duplicate attempts and saved
response recovery; it never calls the webhook itself. Send its body unchanged.
Go exposes `WithOperationTransaction`; Python exposes synchronous
`with_operation_transaction` and asynchronous `awith_operation_transaction`.
See the transaction guide for their examples and commit recovery contract.

Applications managing their own transaction receipts can still construct the
`ManagedOperationResult` and `ManagedOperationEffect` models directly. These
describe the handler response and do not make a second API call.

Names must be unique within a completion and match `[a-z][a-z0-9-]{0,62}`. The
business type must match `[a-z][a-z0-9_.-]*` and contain at most 256 bytes. Each
payload can contain up to 64 KiB of JSON; the whole response, including result
and effects, must fit 1 MiB. Return `effects: []` when no delivery is needed.
The result can be any JSON value, including `null`. Unknown or duplicate control
fields, unsupported versions, invalid effects, and unavailable destinations
fail completion without queueing any effect. Ordinary responses without the
reserved marker retain their existing behavior.

Inspect the operation's usual status URL or run:

```sh
gregale --json operations get <operation-id>
```

The `effects` array contains the name, stable effect and delivery IDs, receiver
ID, generation, business type, status, attempt count, and last error. A
`completed` operation can have a `pending` or `dead` webhook. Retry delivery
through the existing app or tenant webhook delivery retry endpoint; the
business handler does not rerun when Gregale retries a completed effect.

For a managed workflow step, inspect the effect record through
`gregale workflows attempts <run_id> <step_name>`; the attempt response includes
the stable effect/delivery IDs and current delivery status.

The webhook's event type is `operation.effect`, in either the configured JSON
envelope or CloudEvents format. Its business data is:

```json
{
  "operation_id": "<operation UUID>",
  "app_id": "<app UUID>",
  "platform_tenant_id": "<authenticated customer UUID, when scoped>",
  "generation": 1,
  "name": "customer-notification",
  "type": "order.fulfilled",
  "data": {"order_id": 123, "status": "fulfilled"}
}
```

Verify the signature and deduplicate `X-Faas-Delivery-Id` using the existing
[webhook receiver contract](webhook-receiver-verification.md). The delivery ID stays stable
across retries. Delivery is at least once, and receivers can observe a duplicate
after accepting a request if the sender loses the acknowledgement. Suspension,
surface offboarding, receiver disablement, or removal of the explicit app filter
stops future attempts. An in-flight HTTP request cannot be recalled. Deleting a
receiver or pruning its delivery history leaves the effect identity on the
operation with status `unavailable`.

Gregale's result/delivery transaction does not include your application's
database transaction. If an attempt commits business writes and then loses its
response, it can be attempted again. Keep those business writes idempotent using
the operation ID. For workflow steps, an invalid or disabled receiver can also
make Gregale reject the result after the customer transaction committed, so
keep the configured receiver active before dispatch. This adapter does not
provide arbitrary external API calls or interpret job/task stdout or ordinary
workflow responses as effect requests.
