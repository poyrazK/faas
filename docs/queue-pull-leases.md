# Pull leases for worker queues

A push binding delivers each message to the app over HTTP, so the app must
listen for deliveries. A worker or job that runs its own loop with no HTTP
listener consumes a queue by leasing its messages instead: it asks
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
handlers idempotent. A lease that runs out on the message's last attempt moves
the message to the dead-letter queue with outcome code
`queue_lease_attempts_exhausted` as soon as it expires. The ack, nack and
extend calls report each receipt separately in `succeeded` and `failed`.

## Authenticate

- **From inside the worker (recommended):** request a workload-identity token
  for audience `gregale:queues` from the guest's loopback token endpoint. No
  secret is stored in the image, and the platform records which instance
  holds each lease, so a worker that is mid-message is not the one stopped
  when the pool scales in. Only a worker running a production deployment can
  lease with its token: a staging or preview deployment of the same app gets
  `401`, so code reviewed only for a stage never touches production messages.
  Workload tokens need the platform's workload identity. Where it is not
  enabled, the token endpoint answers an error and the SDK consumers report
  `Queue workload identity unavailable`; pass an API key instead.
- **From anywhere else:** an API key with the `queues:consume` scope
  (`deploy:write` and `admin` keys also work).

## Limits

A call leases at most 100 messages and takes at most 100 receipts. It also
stops adding messages once their payloads and headers would pass 8 MiB, but
always returns at least one ready message. A call waits at most 20 seconds.
The default visibility timeout is 30 seconds; the maximum is the plan's async
invocation deadline. A lease cannot be extended past that deadline from when
it was issued either: an extend after it answers `lease_expired`, so a hung
handler cannot hold a message forever. One account holds at most 64 waiting
lease calls at a time; past that a call answers at once without waiting.
If the caller disconnects before a lease response reaches it, the messages
return to the queue without spending an attempt.

Leasing works on production queues only. A stage environment cannot consume
a queue by pull yet: an enabled stage pull binding is refused; use push.

## Retries, retention and binding changes

- A message's retry budget and back-off come from the send's
  `retry_policy`, else the binding's, else the app's, capped by the plan.
  For pull consumers the policy is fixed when the message is sent. Push
  delivery uses the binding's current policy and ignores a per-send one.
- A pending message expires after the plan's retention, counted from its
  send, its last replay, or when a retry or nack delay last made it due,
  whichever is latest. Messages held for their owner (a suspended or
  abuse-held account, a spend cap, a compute budget hold, or the queues
  capability switched off) do not expire while the hold lasts, and have at
  least half the retention left when it lifts.
- Renaming a binding's `queue_name` keeps its messages: they lease and
  settle under the new name, and `queues/state` for the new name counts them.
- Deleting a binding retires it and holds its messages for the plan's
  retention. Creating a binding with the same `name` again revives it and its
  messages, under the queue name the create gives. No other binding may take
  a retired binding's queue name. A dead letter replayed while its binding is
  retired waits in the queue until the binding is revived.
- A lease ends when the worker instance that took it stops or fails: the
  message is redelivered at once, and the attempt counts.

## Long-polls

A lease with `wait_seconds` waits for a message on its queue. An account has
at most 64 waiting leases on one apid; a lease past that cap leases once and,
finding nothing, waits out `wait_seconds` without being woken, so an idle
consumer never polls in a tight loop. A worker token may address its app by
the slug it had before a rename.

The SDK consumers (`consumeQueue` in Node, `QueueConsumer` in Python,
`Client.ConsumeQueue` in Go) extend a lease every third of its visibility
timeout while the handler runs, signal the handler when the lease is lost,
and retry a failed ack or nack.
