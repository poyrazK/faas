# Durable inbound webhooks

Gregale can accept a webhook while your app is scaled to zero. It verifies the
provider request, writes it to the durable invocation queue, and returns `202
Accepted`. The scheduler then wakes the app and POSTs the provider JSON payload
to the configured path. Failed deliveries use the existing async retry and
dead-letter lifecycle.

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

Stripe retries of the same event for the same endpoint return the same receipt
ID with `duplicate: true` and do not enqueue a second delivery.

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
URL. Deleting or disabling an endpoint affects only future ingress; already
accepted deliveries remain in the invocation queue.
