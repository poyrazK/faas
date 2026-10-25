# Pull leases for worker queues

A push binding delivers each message to the app over HTTP, so the app must
listen for deliveries. A worker or job that runs its own loop with no HTTP
listener consumes a queue by leasing its messages instead (ADR-1140): it asks
for messages when it has capacity, and acks or nacks each one.

[Organization queues](organization-queues.md) use the same lease contract and
keep their backlog and receipts through creator departure or erasure.

## Set up

Give the app a **pull** binding for the queue:

```bash
curl -X POST "$GREGALE_API/v1/apps/my-worker/queue-bindings" \
  -H "Authorization: Bearer $KEY" \
  -d '{"name":"jobs","queue_name":"jobs","mode":"pull","workload_class":"worker","max_concurrency":4}'
```

`max_concurrency` caps how many messages can be leased at once across all of
the app's workers. Pair the binding with a `queue_depth` scaling target to
scale the worker pool on the backlog.

## Consume with the SDK

The SDK consumers lease with `wait_seconds`, run your handler, ack on success
and nack (retry under the binding's policy) when it throws. While a handler
runs they extend its lease at half the visibility timeout, and on SIGTERM they
stop leasing and let in-flight messages finish within the app's stop grace
period. Inside a Gregale VM they authenticate with a workload-identity token,
so the image needs no API key.

```js
import { consumeQueue } from '@gregale/sdk-node/preview/queues/runtime';

consumeQueue({ app: 'my-worker', queue: 'jobs', concurrency: 4 }, async (message, { signal }) => {
  await resize(message.payload, { signal }); // signal aborts if the lease is lost
});
```

```python
import asyncio
from faas_sdk.preview.queue_consumer import consume_queue

async def handle(message):
    await resize(message.payload)  # message.lease_lost is set if the lease is lost

asyncio.run(consume_queue(app="my-worker", queue="jobs", handler=handle, concurrency=4))
```

Outside Gregale, pass `apiKey` / `api_key` (a key with `queues:consume`).
`concurrency` is per process; the binding's `max_concurrency` still caps the
whole app.

## Consume over HTTP

```bash
# Lease up to 10 messages, hidden from other consumers for 60 seconds.
# wait_seconds holds the call until a message arrives (at most 20 s).
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"max_messages":10,"visibility_timeout_seconds":60,"wait_seconds":20}'

# Done: complete them.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:ack" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"]}'

# Failed: retry under the binding's retry policy (or dead-letter when the
# attempt budget is spent). delay_seconds can push the retry later.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:nack" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"],"error":"upstream timeout"}'

# Still working: keep the message hidden for another 5 minutes.
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/jobs/leases:extend" \
  -H "Authorization: Bearer $TOKEN" -d '{"receipts":["<receipt>"],"visibility_timeout_seconds":300}'
```

With `wait_seconds`, a call that finds nothing waits for a message and returns
it as soon as one is sent, so a worker can lease in a loop without sleeping.
An empty `messages` list means nothing arrived during the wait; lease again.
Each message is delivered at least once. A lease that is not acked, nacked or
extended before its visibility timeout returns to the queue and is redelivered
with a new attempt; the old receipt then reports `lease_expired`, so make
handlers idempotent. The ack, nack and extend calls report each receipt
separately in `succeeded` and `failed`.

## Authenticate

- **From inside the worker (recommended):** request a workload-identity token
  for audience `gregale:queues` from the guest's loopback token endpoint. No
  secret is stored in the image, and the platform records which instance
  holds each lease, so a worker that is mid-message is not the one stopped
  when the pool scales in.
- **From anywhere else:** an API key with the `queues:consume` scope
  (`deploy:write` and `admin` keys also work).

## Limits

A call leases at most 100 messages and takes at most 100 receipts. It also
stops adding messages once their payloads and headers would pass 8 MiB, but
always returns at least one ready message. A call waits at most 20 seconds.
The default visibility timeout is 30 seconds; the maximum is the plan's async
invocation deadline. Leasing works on production queues; stage environment
queues use their own delivery path.
