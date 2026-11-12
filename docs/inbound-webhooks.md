# Durable inbound webhooks

Gregale can accept a webhook while your app is scaled to zero. It verifies the
provider request, writes it to the durable invocation queue, and returns `202
Accepted`. The scheduler then wakes the app and POSTs the provider JSON payload
to the configured path. Failed deliveries use the existing async retry and
dead-letter lifecycle. An explicitly bound workflow callback is completed
directly instead of creating an app invocation.

Stripe is the first supported provider. Inbound webhooks are available on the
Hobby, Pro, and Scale plans.

## Create an endpoint

```http
POST /v1/apps/my-api/inbound-webhooks
Authorization: Bearer fp_live_...
Content-Type: application/json

{
  "name": "stripe-primary",
  "provider": "stripe",
  "signing_secret": "whsec_...",
  "delivery_path": "/internal/stripe"
}
```

The response contains `endpoint_url`. Copy that URL into Stripe when creating
the webhook destination. The URL is shown only in the create response; Gregale
stores only a hash of its token. The Stripe signing secret is sealed and never
returned.

```json
{
  "id": "...",
  "name": "stripe-primary",
  "provider": "stripe",
  "delivery_path": "/internal/stripe",
  "enabled": true,
  "signing_secret_masked": "***",
  "endpoint_url": "https://api.gregale.dev/v1/hooks/gwh_..."
}
```

## Delivery

After Stripe's signature verifies and the event is durable, Stripe receives:

```http
HTTP/1.1 202 Accepted
Content-Type: application/json

{
  "receipt_id": "...",
  "status": "accepted",
  "duplicate": false,
  "accepted_at": "2026-09-22T15:30:00Z"
}
```

Your app receives a `POST` at `delivery_path` with the Stripe JSON payload.
Gregale adds these headers:

- `X-Faas-Invocation-Source: inbound_webhook`
- `X-Gregale-Webhook-Provider: stripe`
- `X-Gregale-Webhook-Event-Id: evt_...`
- `X-Gregale-Webhook-Endpoint-Id: ...`

The original `Stripe-Signature` is deliberately not forwarded because it may
have expired during a cold start or retry. `X-Faas-Invocation-Source` is
platform-owned and overwritten after queued headers are applied; use it to
distinguish a verified webhook delivery from ordinary public or async traffic.
Treat the `X-Gregale-*` fields as metadata only after checking that source.
Gregale drops `X-Gregale-Webhook-*` and `X-Gregale-Event-*` headers from public
requests, so a caller cannot send them to your app directly or through an
async route.

Stripe retries of the same event for the same endpoint return the same receipt
ID with `duplicate: true` and do not enqueue a second delivery.

Stripe sees `202` as soon as the event is accepted, so Gregale owns the retries
from then on. A handler that throws, or answers 408 or 429, is retried under the
app's `retry_policy`; when the attempts run out, or the handler refuses the
event with another 4xx, the delivery lands in the
[dead letters](dead-letters.md), where you replay it after a fix
(`gregale dlq replay`). Resending the event from Stripe does not deliver it
again: it is the same receipt.

## Backlog

Accepted deliveries wait in the app's queue under the plan's queue caps, the
same caps a queue send meets: pending messages per app and backlog bytes per
account (see [queue limits](event-driven.md#queue-limits)). While either is
full, a verified delivery gets `429 inbound_webhook_backlog_full` with
`Retry-After: 60` and nothing is accepted, so the provider retries the same
event later.

## Rate limit

An account's endpoints accept a bounded number of verified deliveries a
minute: Free 60, Hobby 1,200, Pro 3,000, Scale 6,000. Past it a delivery gets
`429 inbound_webhook_rate_limited` with `Retry-After`. Deliveries that fail
signature verification are limited separately, per client IP.

## Start a stored workflow

An endpoint can start a published stored workflow directly through its
`workflow-binding` resource. This opts into stored workflow delivery for that
endpoint. Signature verification, sealed secrets and endpoint ownership retain
their existing behavior. The durable receipt and captured workflow definition
commit before acknowledgement; no relay handler is needed. See
[stored workflow authoring](stored-workflow-authoring.md#start-from-a-stripe-webhook) for
binding, filters, provider retry behavior and receipt inspection.

## Complete a workflow callback

An account-authorized client can bind a `wait_for_callback` step to one
known Stripe object event:

```http
PUT /v1/workflows/runs/RUN_ID/callbacks/CALLBACK_ID/webhook-binding
Authorization: Bearer fp_live_...
Content-Type: application/json

{"endpoint_id":"ENDPOINT_ID","event_type":"payment_intent.succeeded","object_id":"pi_123"}
```

Use an existing endpoint owned by the same app. Gregale still verifies the
Stripe signature and extracts the event type and `data.object.id`. An exact
match writes the signed JSON as the callback result in the durable workflow
ledger and acknowledges with `{"callback_id":"...","status":"received","duplicate":false}`.
It does not enqueue an app invocation. A signed event that does not match a
binding follows the ordinary delivery path above. A matched event that arrives
after the callback closed is acknowledged with `status: "ignored"` to stop
provider retries. Bind before the event arrives.

The binding can be inspected with `GET` or revoked with `DELETE` at the same
path. The endpoint URL and Stripe secret remain managed through the existing
endpoint API; they are never returned by the binding API. Revocation affects
later deliveries; an already verified request may still finish its callback.

Failed-delivery alerts can use `failure_source: inbound_webhook`; the `any`
source aggregate includes webhook deliveries as well.

## Manage endpoints

```text
GET    /v1/apps/{slug}/inbound-webhooks
GET    /v1/apps/{slug}/inbound-webhooks/{id}
PATCH  /v1/apps/{slug}/inbound-webhooks/{id}
DELETE /v1/apps/{slug}/inbound-webhooks/{id}
```

`PATCH` can enable or disable ingress, change `delivery_path`, or rotate the
provider `signing_secret`. Changing the secret does not change the endpoint
URL. For 24 hours after a rotation, deliveries signed with the old secret
still verify, so you can update the provider after rotating. Deleting or disabling an endpoint affects only future ingress; already
accepted deliveries remain in the invocation queue.
