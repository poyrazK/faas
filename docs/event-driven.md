# Event-driven workloads

Use asynchronous invokes, jobs, webhooks, and scheduled triggers when work does not need to finish in the request path.

```bash
gregale invoke --async --payload @payload.json APP_ID
gregale invoke --async --on-success-webhook WEBHOOK_ID --on-failure-webhook DLQ_WEBHOOK_ID APP_ID
gregale jobs run nightly --tasks 10
gregale crons add --app APP_ID --schedule "0 * * * *" --path /jobs/nightly
```

Handlers receive an event id and delivery attempt. Persist that id before applying side effects so retries are idempotent. Set explicit payload limits, timeouts, retry counts, and retention; route poison messages to a dead-letter destination for inspection and replay.

Keep synchronous endpoints small and return an acknowledgement only after the event is durably accepted. Verify webhook signatures and record the source, schema version, and correlation id in application logs.

Async invocations may name existing app webhook subscriptions as terminal
destinations. The success destination receives a durable `job.finished` event
after completion; the failure destination receives the same envelope when the
invocation permanently fails or exhausts its retry budget. Webhook delivery
has its own retry and dead-letter lifecycle, so a downstream outage does not
change the invocation result.

## Internal event subscriptions

Applications can subscribe to events published through Gregale's internal
event router. A YAML deployment declares subscriptions with `event_triggers`:

```yaml
event_triggers:
  - source: billing.*
    type: invoice.paid
    filter: '{"data":{"amount":{"$gt":100}}}'
```

The filter is a JSON object encoded as a string. `app` is optional for a
single-app deploy and is bound to the target application during source-ref
reconciliation. Event-only projects may use the equivalent TOML form,
`[[triggers.event]]`. Both forms use the same source/type/filter validation;
matching deliveries inherit the router's retry and dead-letter behavior.

Publish an event from the CLI with an explicit event id so producers can
retry safely:

```bash
gregale events publish --id evt-123 --source billing.stripe --type invoice.paid \
  --data '{"amount":150}'
```
