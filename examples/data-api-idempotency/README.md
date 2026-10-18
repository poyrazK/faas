# Idempotent order creation

Run `gregale data-api dev --port 0` in this directory. It generates
`database.types.ts`, exposes the approved `create_order_once` RPC, and verifies
replay, payload conflicts and tenant isolation. Install `@gregale/data` in your
application and use `client.ts` for the typed workflow.

Before the first request, create a UUID using `crypto.randomUUID()` and persist
it with the intended total in your application's pending-operation state. Call
`createOrder(url, token, key, total)`. If the response is lost, cancelled or times
out, first use `reconcileOrder` from `client.ts` with the original key, total,
subject, tenant and API URL. It calls the read-only `read_order_receipt` RPC and
returns `confirmed`, `payload_conflict` or `unresolved`. A new key represents a
new order, so do not generate one for recovery.

Apply the append-only `0002_order_receipt_lookup.sql` migration and grant the
binding execution on `read_order_receipt(uuid,integer)` through the normal owner
RPC permission workflow, then refresh and regenerate types. The lookup works in
a SQL read-only transaction. It derives subject and tenant from the JWT, compares
the stored payload and returns the original receipt without inserting an order.
Another subject or tenant cannot infer a hidden receipt or payload conflict.

If recovery is unresolved, keep the pending operation. The original write may
still be in flight or the receipt may be hidden or removed. A missing receipt
does not prove failure. Any replay is an explicit application decision and must
reuse the same key, payload and identity under the retained-receipt contract.

The PostgreSQL unique constraint scopes keys by authenticated subject and
tenant. The SECURITY INVOKER function derives both claims from the JWT, inserts
once, and returns the stored order. Unique-conflict handling waits for a
competing transaction; under PostgREST's default READ COMMITTED isolation the
following SELECT sees its committed receipt. The insert and receipt are one
transaction, so failed validation or rolled-back work consumes no key.

Identical retries return the same row. Reusing a key with a different total
returns HTTP 409, code `PT409`, message `idempotency_payload_conflict`.
The typed client uses `dataRequest` to preserve server errors and bound waiting;
it does not automatically replay requests. Explicit retries may still encounter
transient lock/statement timeouts; retain the key and reconcile before retrying.

This example deliberately stores the immutable order itself as the response
receipt. A trigger rejects ordinary REST updates and deletes, so those operations
cannot reset a key or change its original response. Receipts have no automatic
expiry. Owner maintenance that removes receipts also ends the guarantee for
those keys; define retention before adopting this pattern for production.
For a separate mutable-business-row pattern with server-enforced expiry, see the
[starter's opt-in v2 contract](../../docs/data-api.md#versioned-replay-expiry).
That contract uses different RPCs and receipt storage; changing this example's
UUID generator alone does not add expiry to `create_order_once`.
For mutable orders, keep a separate immutable receipt recording the original
response and enforce equivalent permissions and transactional writes.

Apply the migration through the hosted owner migration app and grant the
qualified Data API role execution on `create_order_once(uuid,integer)` and
`read_order_receipt(uuid,integer)` using the
existing RPC permission workflow. RLS and restricted role permissions remain
mandatory. Do not use owner credentials for application requests. Idempotency
applies to this RPC and database transaction; external payments, email and other
side effects require their own idempotency or an outbox workflow. If the payload
grows, compare every operation-defining field before returning a cached result.
