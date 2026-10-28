# Workers

A worker is a long-running process with no HTTP listener of its own: a queue
consumer, a stream processor, a scheduler loop. Gregale runs each worker in its
own Firecracker microVM, keeps it running instead of parking it, and scales a
pool of them on the depth of the queues it consumes.

Workers are in preview on the Hobby, Pro and Scale plans.

## Declare a worker

Describe the worker and the queue it consumes in `gregale.yaml`, then
`gregale deploy`. Deploys from a repository or GitHub apply the same file.

```yaml
schema_version: 1
worker:
  drain_timeout: 30s
  scale:
    min: 0
    max: 3
    metric: queue_depth
    target: 100
queue_bindings:
  - name: jobs
    queue_name: jobs
    max_concurrency: 4
```

The `worker` block makes the app a worker and sets how its pool scales (see
[Scale on the backlog](#scale-on-the-backlog)). A binding in `queue_bindings`
is a **pull** binding by default: the worker leases messages when it has
capacity and acks or nacks each one. A worker is not reaped when idle; if its
process exits, guest-init restarts it under the restart policy (see
[declarative worker lifecycle](cli/deploy.md#declarative-worker-lifecycle)).

## Send messages

Any app, script or CI job sends to the worker's queue by name:

```bash
gregale queue send --app my-worker --queue-name jobs --payload '{"image":"cat.png"}'
```

```bash
curl -X POST "$GREGALE_API/v1/apps/my-worker/queues/send" \
  -H "Authorization: Bearer $KEY" \
  -d '{"queue_name":"jobs","payload":{"image":"cat.png"}}'
```

A send that would pass a plan limit is refused; see [Limits](#limits).

## Consume a queue

Consume the queue with the SDK, which leases, runs your handler, acks on
success, nacks on an error and keeps long-running leases alive:

```js
import { consumeQueue } from '@gregale/sdk-node/preview/queues/runtime';

consumeQueue({ app: 'my-worker', queue: 'jobs', concurrency: 4 }, async (message, { signal }) => {
  await processJob(message.payload, { signal });
});
```

```python
import asyncio
from faas_sdk.preview.queue_consumer import consume_queue

async def handle(message):
    await process_job(message.payload)

asyncio.run(consume_queue(app="my-worker", queue="jobs", handler=handle, concurrency=4))
```

Inside Gregale the SDK authenticates with a workload-identity token, so the
image holds no API key. A **push** binding instead delivers each message to a
worker that listens on its port. [Pull leases](queue-pull-leases.md) covers the
HTTP API, retries, visibility timeouts and authentication in detail.

Messages are delivered at least once: make handlers idempotent.

## Scale on the backlog

`worker.scale` says how many waiting messages one worker should take:
`metric: queue_depth` with `target: 100` runs `ceil(depth / 100)` workers,
between `min` and `max`, and never more than the binding's `max_concurrency`
allows. With `min: 0` the pool stops when the queue is empty and starts a
worker when the next message arrives. A worker that reads an external broker
(Kafka, NATS, SQS and others) through `worker.source` scales on
`metric: queue_lag` instead. A `scaling` block with a `queue_depth` target
works the same way, with `min_instances` as the floor. It scales out on the tick that sees the
backlog and scales in only after demand has stayed low for 60 seconds, so a
queue that drains and refills does not restart workers. A worker that holds
unexpired leases is kept when the pool shrinks.

On the stop signal, stop leasing and finish or nack in-flight messages within
`drain_timeout`. The SDK consumers do this for you; an unacked lease is
redelivered after its visibility timeout.

## Inspect and operate

```bash
gregale workers                     # worker apps and their pools
gregale worker status my-worker    # replicas, scaling metric, backlog, dead letters
gregale worker logs my-worker --follow
gregale worker scale my-worker --min 0 --max 3 --target 100
```

To hear when a worker falls behind, alert on `queue_backlog` or
`queue_oldest_age_seconds` (see [queue backlog alerts](alerts.md#queue-backlog-alerts)).

## Limits

| | Hobby | Pro | Scale |
|---|---|---|---|
| Running workers per account | 1 | 3 | 10 |
| Worker RAM | up to 512 MB | up to 1 GB | up to 2 GB |
| Pending messages per app | 10,000 | 100,000 | 1,000,000 |
| Pending message bytes per account | 100 MiB | 1 GiB | 10 GiB |
| How long a message waits for a worker | 1 day | 4 days | 14 days |

A message that waits longer than its plan allows moves to the dead-letter queue,
where `gregale dlq <app>` lists it for replay. See
[queue limits](event-driven.md#queue-limits) for message size, delivery
attempts and the errors a send returns at a limit.

## Billing

A worker bills like any instance: its plan RAM plus 8 MB for every second it
runs. CPU is included in the RAM price. A pool that has scaled to zero costs
nothing.
