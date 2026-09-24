# queue-worker

A minimal Node.js push consumer for Gregale's durable queue. The manifest
declares its queue binding and a bounded retry policy, so deployment does not
require a separate consumer.

## Start here

```sh
gregale init --template queue-worker --path ./orders-worker
cd orders-worker
gregale deploy --name orders-worker
```

The default binding consumes the `default` queue through this HTTP function.
The current trigger dispatches one message at a time per binding; it does not
provide per-worker parallel delivery or queue-depth autoscaling. Edit
`gregale.yaml` before deploying if your queue name or retry budget differs.

## Send and inspect work

```sh
gregale queue send orders-worker --payload '{"job_id":"order-123"}'
gregale queue status orders-worker --json
```

The handler receives the parsed JSON payload as `event.body` (or a raw JSON
string) and returns `202` after
accepting it. Keep the business operation idempotent because retries and
redelivery are normal queue behavior. Exhausted messages are visible with
`gregale dlq orders-worker` and can be replayed after the underlying error is
fixed.

## Re-deploy after edits

Edit `handler.js` or `gregale.yaml`, then run:

```sh
gregale deploy
```
