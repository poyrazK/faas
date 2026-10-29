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
The starter dispatches one message at a time with `max_concurrency: 1`.
Increasing that binding setting enables bounded parallel HTTP deliveries.
This function does not use worker queue-depth autoscaling. Edit
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
`gregale dlq list orders-worker` and can be replayed after the underlying error is
fixed.

## Verify handler completion before release

The reserved `gregale_queue_canary: "v1"` message takes a safe no-op path before
ordinary job handling. It returns success after that path finishes and never
processes a business job. Malformed canaries, unexpected fields and bindings
outside the deployment's declared capability fail. This proves delivery and
handler completion; it does not prove your application's business effects.

Enable bounded deployment pinning, then opt in for the exact deployment:

```sh
gregale app orders-worker --revision-pin-ttl 10m
gregale deploy --name orders-worker --queue-canary-binding default
```

Use `--dry-run --json` to preview the declared capability before deploying.
The flag validates the selected source manifest and sets deployment-specific
`GREGALE_QUEUE_CANARY_BINDINGS`; it does not publish a message. An app-wide
environment setting alone does not declare this capability. Repeat the flag
for each binding and on each new deployment that should support verification.
Omitting it on redeployment does not carry forward the previous opt-in.
Use the returned deployment UUID below:

```sh
gregale app binding release-policy set orders-worker --scope default --require-verification --queue-require default=handler_completion --expected-revision 0
gregale app binding verify orders-worker --deployment DEPLOYMENT_UUID --release-policy --dry-run --json
gregale app binding verify orders-worker --deployment DEPLOYMENT_UUID --release-policy --durable --queue-publish --json
```

The final command explicitly consents to publishing a safe test message. Save
its run ID and resume it with `gregale app binding verification-runs resume
orders-worker RUN_ID --json` after a disconnected session. Resume retains the
original message and deadline. Verification does not promote traffic; promotion
rechecks fresh completion evidence. Cancelled, expired or other-deployment
evidence cannot satisfy the release requirement.

## Re-deploy after edits

Edit `handler.js` or `gregale.yaml`, then run:

```sh
gregale deploy
```
