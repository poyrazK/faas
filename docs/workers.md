# Workers

A worker is a long-running process with no HTTP listener of its own: a queue
consumer, a stream processor, a scheduler loop. Gregale runs each worker in its
own Firecracker microVM, keeps it running instead of parking it, and scales a
pool of them on the depth of the queues it consumes.

Workers are in preview on the Hobby, Pro and Scale plans.

## Declare a worker

Set the execution mode in your app's manifest, or pass
`gregale deploy --execution-mode worker` once:

```yaml
schema_version: 1
lifecycle:
  execution_mode: worker
  restart_policy: always
  startup_deadline_s: 30
  stop_grace_period_s: 30
```

A worker is not reaped when idle. If it exits, the restart policy decides
whether it starts again. See [declarative worker lifecycle](cli/deploy.md#declarative-worker-lifecycle)
for every lifecycle field.

## Consume a queue

Bind the worker to a queue. A **pull** binding fits a worker with no listener:
the worker leases messages when it has capacity and acks or nacks each one.

```bash
curl -X POST "$GREGALE_API/v1/apps/my-worker/queue-bindings" \
  -H "Authorization: Bearer $KEY" \
  -d '{"name":"jobs","queue_name":"jobs","mode":"pull","workload_class":"worker","max_concurrency":4}'
```

Then consume it with the SDK, which leases, runs your handler, acks on success,
nacks on an error and keeps long-running leases alive:

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

Add a `queue_depth` target to say how many waiting messages one worker should
take:

```yaml
scaling:
  min_instances: 0
  max_instances: 3
  targets:
    - metric: queue_depth
      value: 100
```

The pool runs `ceil(depth / value)` workers, between `min_instances` and
`max_instances`, and never more than the binding's `max_concurrency` allows.
`value` is at most 10,000 messages per worker.
With `min_instances: 0` the pool stops when the queue is empty and starts a
worker when the next message arrives. It scales out on the tick that sees the
backlog and scales in only after demand has stayed low for 60 seconds, so a
queue that drains and refills does not restart workers. A worker that holds
unexpired leases is kept when the pool shrinks.

On the stop signal, stop leasing and finish or nack in-flight messages within
`stop_grace_period_s`. The SDK consumers do this for you; an unacked lease is
redelivered after its visibility timeout.

## Inspect and operate

```bash
gregale workers                     # worker apps and their pools
gregale worker status my-worker    # replicas, scaling metric, backlog, dead letters
gregale worker logs my-worker --follow
gregale worker scale my-worker --min 0 --max 3 --target 100
```

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
