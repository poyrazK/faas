# queue-worker

A minimal Node.js push worker for Gregale's durable queue. The manifest
declares the queue binding, queue-depth autoscaling, and a bounded retry
policy, so deployment does not require a separate consumer or autoscaler.

## Start here

```sh
gregale init --template queue-worker --path ./orders-worker
cd orders-worker
gregale deploy --name orders-worker
```

The default `queue` binding consumes the `default` queue with one concurrent
delivery per worker. It scales from zero to ten workers at roughly ten pending
messages per worker. Edit `gregale.yaml` before deploying if your queue name,
capacity, or retry budget differs.

## Send and inspect work

```sh
gregale queue send orders-worker --payload '{"job_id":"order-123"}'
gregale queue status orders-worker --json
```

The handler receives the JSON payload as `event.body` and returns `202` after
accepting it. Keep the business operation idempotent because retries and
redelivery are normal queue behavior. Exhausted messages are visible with
`gregale dlq orders-worker` and can be replayed after the underlying error is
fixed.

## Re-deploy after edits

Edit `handler.js` or `gregale.yaml`, then run:

```sh
gregale deploy
```
