# Verify outbound Gregale webhooks

Use the SDK verifier with the exact request body bytes and the three signed
headers. Do this before parsing or re-serializing JSON.

Gregale computes HMAC-SHA256 over:

```text
<unix_timestamp>.<delivery_id>.<raw_body>
```

The signature is sent as `sha256=<hex>` in
`X-Faas-Webhook-Signature`. The other required headers are
`X-Faas-Webhook-Timestamp` and `X-Faas-Delivery-Id`. The SDKs reject duplicate
or malformed header values, compare signatures in constant time, and enforce
a five-minute timestamp tolerance by default. Each retry gets a fresh
timestamp; the delivery ID remains stable across retries.

## Go

Read and bound the raw body before verification:

```go
body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
if err != nil {
    http.Error(w, "request body too large or unreadable", http.StatusBadRequest)
    return
}

verified, err := faas.VerifyWebhook([]byte(webhookSecret), r.Header, body)
if err != nil {
    http.Error(w, "invalid webhook", http.StatusUnauthorized)
    return
}

// Use verified.DeliveryID as the unique receipt key. Decode body only after
// verification, and record the receipt with the business change in one DB tx.
```

`VerifyWebhookWithOptions` lets a receiver set a different timestamp tolerance.

## Node.js

Read the request body once, before middleware consumes or transforms it:

```ts
import { verifyWebhook } from '@gregale/sdk-node';

const rawBody = Buffer.from(await request.arrayBuffer());
const verified = verifyWebhook(webhookSecret, request.headers, rawBody);
const event = JSON.parse(rawBody.toString('utf8'));

// Insert verified.deliveryId under a UNIQUE constraint and apply the event's
// side effect in the same transaction. A previously recorded ID is a duplicate.
```

The Node helper accepts a `Headers` object or Node's incoming header map.
`VerifyWebhookOptions` supports a custom `timestampToleranceMs`.

## Python

With an async request API, read raw bytes before decoding JSON:

```python
from faas_sdk import verify_webhook

raw_body = await request.body()
verified = verify_webhook(webhook_secret, request.headers, raw_body)
event = json.loads(raw_body)

# Insert verified.delivery_id under a UNIQUE constraint and apply the event's
# side effect in the same transaction. A previously recorded ID is a duplicate.
```

Pass a custom `timestamp_tolerance` when your receiver needs a different
freshness window.

## Acknowledge only durable work

Treat webhook delivery as at-least-once. Use a database unique constraint on
the verified delivery ID and store that receipt in the same transaction as the
business side effect. If the ID already exists, return a successful response
without repeating the side effect. Return a non-2xx response when the event
could not be durably accepted so Gregale can retry it.

Timestamp validation limits how long an intercepted request can be replayed;
it does not replace delivery-ID deduplication. For CloudEvents deliveries, the
envelope `id` is also available, while `X-Faas-Delivery-Id` identifies the
specific receiver delivery.
