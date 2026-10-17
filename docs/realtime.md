# Managed realtime

Gregale manages client connections and channel delivery. Your backend publishes
messages to a managed realtime endpoint; web and mobile clients subscribe through
WebSocket or HTTPS Server-Sent Events. This supports job progress, live dashboards,
chat events, and notifications without operating a separate connection server.

Configure the endpoint and authentication before distributing client credentials.
Keep account credentials on your backend. Scoped publisher credentials can limit
channels, event types, payload sizes, message TTL, and publishing rate. Worker
credentials authorize only their configured consumer group and operations.

Retained channels support recovery after reconnecting. Consumer groups add leased
claims, retry policies, dead-letter recovery, and worker health reporting. A worker
must settle a claim with its current fencing token; use that token to protect
customer-side writes from a stale worker.

See the [Node SDK](../sdk/node/README.md), [Python SDK](../sdk/python/README.md),
and [customer transaction and fencing examples](realtime-customer-examples.md).
The [realtime operations guide](ops/realtime.md) documents configuration,
preview gates, limits, recovery behavior, and rollout procedures.

## Fair-use limits

Realtime has no separate charge. Gregale bills compute, and realtime channels,
consumer groups and delivery are included in every paid plan. Limits apply only
where realtime holds real capacity on our servers:

| Plan | Endpoints per app / account | Concurrent connections per account |
|---|---:|---:|
| Hobby | 2 / 10 | 200 |
| Pro | 10 / 50 | 1,000 |
| Scale | 25 / 250 | 5,000 |

- **Connections** count every open WebSocket and SSE connection across all of
  an account's endpoints. Each holds memory on a realtime node for as long as it
  is open. A refused connection gets HTTP 429 with code
  `realtime_connection_limit`, the limit and the number open. Close idle
  connections, share one connection per browser tab, or upgrade the plan.
- **One node, one account:** an account may hold at most half of a single
  realtime node's connections, so one busy account cannot fill a node. This
  refusal names the per-node limit; retry with backoff, and new connections may
  land on another node.
- **Retained history** is bounded per endpoint (24 hours, 1,024 messages per
  channel, 4 KiB per message).
- **Functions triggered by channel messages** bill as normal compute.

An endpoint's `max_connections` still caps that endpoint; the account limit
applies on top of it.

See where you stand with `gregale realtime usage` (or `GET /v1/usage/realtime`):
connections open now against your limit, and, where usage recording is on, the
peak connections, connection-hours and messages for each recent hour, per
account and per endpoint.

## Subscription tickets

A subscription ticket lets your backend hand a browser or app a short-lived
(up to five minutes) credential for named channels without an OIDC provider.
Each ticket comes with a `ticket_id`: store that, not the ticket, and revoke the
ticket early with `DELETE …/tickets/{ticket_id}`, or revoke all of a user's
tickets at logout with `DELETE …/tickets?principal=…`. Connections using a
revoked ticket close within about a second. See the
[ticket operations guide](ops/realtime.md#short-lived-subscription-tickets-operator-preview).

## Presence

Presence shows who is on a channel: every resumable WebSocket subscriber joins
the channel's presence with an optional JSON state (up to 512 bytes), such as
`{"status":"online"}` or a cursor position. Other subscribers get a snapshot
when they subscribe, then `joined`, `updated` and `left` events. A client
changes its state with a `presence` frame, at most 10 times a second.

```json
{"type":"subscribe","channel":"room-a","after":0,"state":{"status":"online"}}
{"type":"presence","channel":"room-a","state":{"status":"typing"}}
```

- By default each connection is its own member. Add
  `"presence_scope": "principal"` to group a signed-in user's tabs and devices
  into one member; `connection_count` says how many sockets it has, and the
  principal itself is never shown to other clients.
- Presence works across realtime nodes. A crashed node's members disappear
  within 45 seconds; a normal unsubscribe or close removes them at once.
- A channel holds up to 512 present connections. Presence state comes from the
  client, so treat it as display data, not identity.
- Read-only ticket subscribers do not join presence.

With the Node SDK, pass `presence` (initial state), `presenceScope` and
`onPresence` to `consumeRealtimeChannel`, and update state through the actions
passed to `onSubscribed` (`updatePresence`). The
[realtime operations guide](ops/realtime.md) describes the v2 presence and
signal frames in full, including transient signals such as typing indicators.

## Channel occupancy

Occupancy tells you how many clients are on a channel right now, for "23
watching" badges, room sizes or dashboards (ADR-1324). Ask for up to 20
channels at once:

```bash
gregale realtime occupancy my-app EP_ID lobby game-42
# CHANNEL  SUBSCRIBERS  PRESENCE MEMBERS
# lobby             23                19
# game-42            4                 4

curl -H "Authorization: Bearer $GREGALE_TOKEN" \
  "https://api.gregale.dev/v1/apps/my-app/realtime/endpoints/$EP_ID/occupancy?channel=lobby&channel=game-42"
```

- `subscribers` counts every open subscription to the channel across all
  realtime nodes: WebSocket (both protocols), read-only ticket and SSE. A
  connection subscribed once counts once.
- `presence_members` counts distinct presence members. A member grouped by
  principal (`presence_scope: "principal"`) counts once however many tabs it
  has open.
- Counts are a snapshot. If a realtime node cannot be asked, the response says
  `"partial": true` with `nodes_unavailable`, and the counts cover the nodes
  that answered.

Occupancy reads are free and need read access to the app.

### Live counts in the browser

A client on the resumable WebSocket protocol can receive the count as it
changes instead of polling. Add `"occupancy": true` to the subscribe frame, or
pass `onOccupancy` with the Node SDK:

```ts
await consumeRealtimeChannel({
  url, webSocketFactory, channel: 'lobby', cursorStore,
  onMessage: render,
  onOccupancy: ({ subscribers }) => setWatching(subscribers),
});
```

The server sends `{"type":"occupancy","channel":"lobby","subscribers":23}`
after subscribing and whenever the count changes, at most once a second. The
count includes this connection. Read-only ticket subscribers can ask for it
too; SSE streams count but do not receive live counts yet. If a realtime node
already follows its maximum of 1,000 live-count channels, the subscription
still works and the client gets an `occupancy_limit` error frame instead.

## Principal access controls preview

When the operator enables the access-control preview, an endpoint can deny a
principal access to specific channels or revoke its existing sessions. OIDC
endpoints use the authenticated principal; subscription-ticket authentication
requires the ticket preview to be enabled as well. Access controls do not permit
clients to choose another principal or override their credential's permissions.

Access-control capacity is bounded. If the endpoint reaches its limit, review
existing deny entries before adding more. See the
[access-control operations guide](ops/realtime.md#principal-access-controls-preview)
for the request contract and recovery procedure.

## Function triggers

A realtime trigger runs one of your functions for each message on a channel, so
you do not operate always-on workers (ADR-1322). It consumes an existing consumer
group: create the group with `lease_seconds` of at least 120, then create a
trigger with `kind: realtime` and `{"endpoint_id", "channel", "group"}` config.
The group's ordering, filters, retry policy and dead-letter queue apply, and a
parked app wakes for the first message.

Each message is one invocation. A 2xx response completes it; a failed or
timed-out handler retries under the group's retry policy; a platform failure
returns the message without spending an attempt. While a trigger consumes a group,
direct claims return 409 `realtime_group_bound_to_trigger`; deleting the trigger
returns the group to your workers.

Declare it in `gregale.yaml` to create it on deploy, in a single-app manifest or
for one function of a [multi-function project](functions.md):

```yaml
triggers:
  - kind: realtime
    app: notify                   # the app slug, or a function name
    slug: orders
    config:
      endpoint_id: 6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11
      channel: orders
      group: billing
      max_concurrency: 4          # optional; defaults to 1
```

Deploy matches the app's realtime triggers to the file by `slug` and rolls the
change back if the deploy fails. See the
[function-trigger operations guide](ops/realtime.md#realtime-function-triggers).

### Several messages per invocation

On a busy channel, set `delivery: batch` (with an optional `batch_size_max` up
to 16, default 10) so one invocation handles several messages, which costs
less compute than one invocation each (ADR-1321). The same option works for
Kafka, NATS, Redis Streams and SQS-compatible triggers; it is not available for
cron or queue triggers. Your function receives:

```json
{"records": [
  {"item_identifier": "…", "data": {"endpoint_id": "…", "channel": "orders", "sequence": "42", "…": "…"}},
  {"item_identifier": "…", "data_base64": "…"}
]}
```

`data` holds a JSON payload as-is and `data_base64` any other payload, with
header `X-Gregale-Batch-Size`. Return a 2xx response to complete every record,
or list the ones to retry:

```json
{"batchItemFailures": [{"itemIdentifier": "…"}]}
```

An error or non-2xx response retries the whole batch. For realtime triggers the
group's ordering still holds: messages with the same ordering key never share a
batch while one of them is unfinished.

```yaml
triggers:
  - kind: realtime
    app: notify
    slug: orders
    delivery: batch
    batch_size_max: 10
    config: {endpoint_id: 6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11, channel: orders, group: billing}
```

`gregale triggers add --delivery batch` and `PATCH /v1/triggers/{id}` with
`{"delivery": "batch"}` set it outside a manifest.
