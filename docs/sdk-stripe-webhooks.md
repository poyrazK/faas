# Typed Stripe webhook pipelines (preview)

A verified Stripe payment can stage a generated business event in the same
PostgreSQL transaction as the business update. The existing event outbox and
[event pipeline](sdk-event-pipelines.md) then publish and start a durable Job
pipeline. Node and Python share the receipt, identity and SQL protocol.

The first adapter supports Stripe snapshot events delivered by Gregale's native
inbound-webhook endpoint. Native ingress owns raw-byte signature verification,
secret rotation, durable queueing and provider-ID receipt conflict detection.
This adapter adds application authorization, business projection and durable
handoff into typed pipelines. It does not create a webhook endpoint or fetch a
Stripe object. Thin events require a separate trusted snapshot resolver.

## Setup and generated types

Upgrade the SDK, opt into `@gregale/sdk-node/preview` or `faas_sdk.preview`, and
configure a native **Stripe app-delivery endpoint** for `/webhooks/stripe` using
the existing inbound-webhook management API. Pin its UUID, Stripe API version,
mode and optional connected account. The endpoint's stored-workflow binding is
a separate native feature; this adapter uses app delivery and the event outbox.

Use your application's migration role to install
[stripe-webhook.sql](../sdk/request-contracts/stripe-webhook.sql) and the
[event outbox schema and upgrades](sdk-event-outbox.md). All business and SDK
receipt/outbox tables must use the same primary PostgreSQL database. Receipt
DDL is exported as `stripeWebhookSchema` / `STRIPE_WEBHOOK_SCHEMA`; construction
never runs migrations. Use an application-scoped database role and explicit
statement/lock limits. This table belongs to the application, not the platform.

Generate the **business event payload** from its contract:

```sh
gregale-event-types event-contract.json typescript event-generated.ts
python -m faas_sdk.preview.event_types event-contract.json python event_generated.py
```

Configure `createStripeWebhookAdapter(contract, definition)` in Node or
`StripeWebhookAdapter(contract, **definition)` in Python. Node definitions use
`endpointId`, `deliveryPath`, `apiVersion`, `eventTypes`, `connectedAccountId`
and `retryWindowMs`; Python uses snake case and `retry_window` in seconds.
Both require `name`, `scope`, `livemode`, `pool`, server-side `token`, and three
callbacks:

- `verify`: authenticate the trusted platform delivery boundary, then return
  `stripeDeliveryContextFromHeaders(headers, rawBody, accountId, appId)` or
  `stripe_delivery_context_from_headers(...)`. These header helpers **do not
  authenticate a request**. Preserve original bytes; do not parse/re-encode them.
  Reject ordinary public traffic and spoofed protected headers before calling
  a helper. Native delivery headers attest a signature checked at ingress;
  the original Stripe signature is not forwarded and may be stale after wake.
- `authorize`: look up the payment/customer in trusted application data, check
  the authenticated principal's **current** access, and return the current owner
  or null/None. Take a business-row lock for mutations. Stripe metadata and
  caller-supplied owner IDs cannot grant access. This runs for duplicate handle
  calls and every inspection as well as first delivery.
- `apply`: make business SQL changes and return `{state:'stage',input:payload}`
  / `StripeWebhookStage(input=payload)`, or an ignore decision. Python's generated
  payload type and `StripeWebhookDecision[Input]` retain static type checking;
  both SDKs validate the payload at runtime before staging.

Callbacks receive the branded transaction and a snapshot event with `id`, `type`,
`api_version`, `livemode`, `created`, optional `account`, and `data_object`.
Node also passes an AbortSignal. Python uses task cancellation. All callbacks
must finish within the operation deadline. `authorize` and `apply` must perform
SQL only: do not call Stripe, publish events, send email or start Jobs inside
these callbacks. A callback that performs an external effect cannot roll it back.

See the working [Node](../examples/sdk-csv-jobs/paid-producer.mjs) and
[Python](../examples/sdk-csv-jobs/paid_producer.py) implementations for a locked
payment lookup, exact integer-cent price/currency/customer checks, immutable
import input, and a current-owner check independent of provider metadata.

## Delivery and recovery

```ts
const result = await adapter.handle(delivery, principal, {
  timeoutMs: 10_000, signal,
});
if (result.outcome === 'staged') {
  // receipt.outbox_key identifies committed local intent.
  // A separate bounded event-outbox worker publishes it.
}
const receipt = await adapter.inspect(delivery, principal, {timeoutMs: 10_000});
```

```python
result = await adapter.handle(delivery, principal, timeout=10)
receipt = await adapter.inspect(delivery, principal, timeout=10)
```

`delivery` includes POST method, exact configured path, protected headers and raw
bytes. Both SDKs verify the native account/app and current enabled Stripe
endpoint before handling. The event must match the pinned API version, mode,
connected account and native event/body attestations. Explicit native replay
lineage keeps the original business identity, with int64 generations parsed as
strings. Duplicate/malformed headers, mismatched hashes and unsafe JSON numbers
fail closed. Unsupported event types return `ignored/event_type` without a local
receipt or business callback; native ingress has already retained its receipt.

One `(account, app, endpoint, Stripe event ID)` can commit one local decision.
Concurrent Node/Python calls serialize on the same identity, then lock the
business row. Business SQL, generated event intent and the payload-free handoff
receipt commit together. Repeated delivery returns the committed decision with
`replayed=true` after current authorization. Different bytes or adapter bindings
under the same identity produce `conflict`; changing the adapter name cannot
bypass deduplication.

Separate Stripe events can describe the same business effect, and delivery order
is not a business-state ordering guarantee. Check locked current payment state;
ignore an already-applied payment even if the new provider event ID differs.
Ignore reasons are `not_applicable`, `already_applied`, `cancelled`, `superseded`.
The sample never downgrades paid state when an older failure arrives.

A `staged` result means **local intent committed**, not publication or pipeline
completion. Run the existing bounded outbox worker, then use the shared pipeline
start receipt and typed pipeline run to observe completion. The adapter never
performs hidden network retries or automatically reruns a business callback.

After `commit_unknown`, retain the identity and original verified delivery and
call `inspect`. Inspection takes the same transaction lock so it waits for an
in-flight commit before reading. It rechecks account/app and current owner;
endpoint disable/delete does not prevent authorized historical inspection.
A retained receipt resolves the local handoff only. A null result means no
committed local handoff was observed; an explicit retry of the same delivery is
safe against the shared identity. If inspection times out, keep the outcome
unknown. Deadline/cancellation while a transaction remains unresolved may be
reported conservatively as `commit_unknown`. Before SQL starts, deadlines and
cancellation cannot stage work. Late callback transaction handles are fenced.

Receipts retain owner, identity, disposition, outbox key, reason and exact
microsecond timestamps. They exclude event payload, credentials and claim tokens.
The table stores only configuration/body digests for conflict detection. The
existing outbox owns payload retention/erasure. Changing endpoint, event schema,
API version, accepted types, mode, account or retry window needs an explicit
migration/drain strategy; old receipts never silently adopt new definitions.
Secret rotation is native and does not change the adapter binding.

The default retry window is one day, configurable from one second to one day.
This is a conservative SDK bound; native quotas and retention remain authoritative.
Receipts have no automatic pruning: deleting them prematurely weakens deduplication.
External effects remain at least once and need their own idempotency mechanism.

## Local evidence

[The paid CSV flow](../examples/sdk-csv-jobs/README.md#stripe-paid-reviewed-imports)
checks signature rejection/rotation, dropped ingress acknowledgement, conflicting
provider bytes, forged deliveries, two-user authorization, concurrent SDKs,
separate provider IDs, reordered failure and revoked access. It then recovers a
lost publication reply and lost pipeline-start reply, completes mixed workers,
opens private browser approval and verifies the report.

Shared unit vectors cover bindings, UUIDv5 receipts, timestamps, rollback,
uncertain commits, scope pins, cancellation and deadlines. Installed npm/wheel
consumers check exports and generated-payload/principal types. Local acceptance
uses real PostgreSQL and Chrome with native API/ingress protocol fixtures; it
is not a deployed Stripe integration or broker/VM acceptance test.
