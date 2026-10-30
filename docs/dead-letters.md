# Dead letters

Work that keeps failing ends in one dead-letter ledger per app, whatever
produced it. You can inspect each entry, replay it once you have fixed the
cause, or purge it.

| Source | What lands there | Shown as |
|---|---|---|
| `invocation` | async invocations, queue messages, delayed tasks and event subscription deliveries whose handler failed on every attempt | `origin` is `event_subscription` for event deliveries |
| `trigger` | broker trigger records (Kafka, NATS, Redis Streams) that exhausted their attempts or had no stable identity | the trigger's id |
| `webhook` | outbound webhook deliveries that exhausted their retries | the webhook's id |
| `event_route` | published events that could not be routed to a subscription | not replayable; see [routing failures](events.md#routing-failures-in-the-dead-letter-queue) |

An event delivery is retried with backoff before it is dead-lettered; see
[Internal event subscriptions](events.md#internal-event-subscriptions).
Entries are kept for 30 days.

## From the CLI

```bash
gregale dlq list my-api                     # newest first, every source
gregale dlq list my-api --source trigger    # only one source
gregale dlq inspect my-api <event-id>       # payload, headers and the last error
gregale dlq replay my-api <event-id>        # put one entry back to work
gregale dlq replay my-api --all             # replay up to --limit pending entries
gregale dlq purge my-api <event-id>         # delete one entry
gregale dlq purge my-api --all              # delete every entry (asks first)
```

A replay keeps the original payload and identity, so a handler that is
idempotent for the first attempt is idempotent for the replay. An entry
is marked replayed and is not replayed twice.

## From the API

| Method and path | What it does |
|---|---|
| `GET /v1/apps/{slug}/dlq` | list an app's dead letters (`limit`, `before`) |
| `GET /v1/apps/{slug}/dlq/{id}` | inspect one |
| `POST /v1/apps/{slug}/dlq/{id}/replay` | replay one |
| `POST /v1/apps/{slug}/dlq:replay_all` | replay up to `limit` pending entries |
| `DELETE /v1/apps/{slug}/dlq/{id}` | purge one |
| `DELETE /v1/apps/{slug}/dlq` | purge every entry |
| `GET /v1/account/dlq`, `/v1/account/dlq/{id}` | the same across every app you can see |
| `POST /v1/account/dlq/{id}/replay`, `POST /v1/account/dlq:replay_all` | replay across apps |
| `DELETE /v1/account/dlq/{id}`, `DELETE /v1/account/dlq` | purge across apps |

Reading needs a key with read scope. Replaying and purging need
`deploy:write`.

## In the dashboard

**Failed events** lists the dead letters of every app you can see, newest
first. Filter it to one app, replay or discard one entry, a selection, or
everything shown.
