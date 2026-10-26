# event-worker

A minimal Node.js worker for Gregale's internal event router. The template
ships with one `event_triggers` declaration and a handler that acknowledges
the canonical event envelope without logging the payload.

## Start here

Internal events (publishing, `event_triggers` subscriptions, acknowledgments
and delivery controls) are a preview capability. Until it is released to your
account, a deploy that declares `event_triggers` answers `403
capability_not_released`. Run `gregale previews list` to see whether you can
join it with `gregale previews join <key>`, or contact support for access.

```sh
gregale init --template event-worker --path ./invoice-worker
cd invoice-worker
gregale deploy --name invoice-worker
```

The default subscription matches `billing.*` events of type
`invoice.paid` whose `data.amount` is greater than 100. Edit
`gregale.yaml` before deploying if your source, type, or filter differs.

## Publish and inspect

Preview the account-wide route first; this checks the worker's filter without
creating an event or invocation:

```sh
gregale events preview billing.stripe invoice.paid \
  --data '{"amount":150}'
```

Then publish and inspect the reconciled subscription:

```sh
gregale events publish billing.stripe invoice.paid \
  --data '{"amount":150}'
gregale invocations list --limit 10
gregale events subscriptions invoice-worker
```

The handler receives the full event envelope as its parsed JSON request body
(`event.body`; a raw JSON string is accepted too). It rejects malformed
envelopes instead of acknowledging and discarding them. Keep event
processing idempotent: Gregale retries asynchronous deliveries and exposes
terminal failures through `gregale dlq list invoice-worker` for inspection and
replay.
