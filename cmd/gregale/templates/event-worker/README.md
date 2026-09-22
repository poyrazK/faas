# event-worker

A minimal Node.js worker for Gregale's internal event router. The template
ships with one `event_triggers` declaration and a handler that acknowledges
the canonical event envelope without logging the payload.

## Start here

```sh
gregale init --template event-worker --path ./invoice-worker
cd invoice-worker
gregale deploy --name invoice-worker
```

The default subscription matches `billing.*` events of type
`invoice.paid` whose `data.amount` is greater than 100. Edit
`gregale.yaml` before deploying if your source, type, or filter differs.

## Publish and inspect

```sh
gregale events publish billing.stripe invoice.paid \
  --data '{"amount":150}'
gregale invocations list --limit 10
gregale events subscriptions invoice-worker
```

The handler receives the full event envelope as its request body. Keep event
processing idempotent: Gregale retries asynchronous deliveries and exposes
terminal failures through `gregale dlq invoice-worker` for inspection and
replay.
