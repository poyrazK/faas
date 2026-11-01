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

Some features on this page are previews: they become available to your account
when Gregale releases them to it. Until then their API routes answer 404 and
their frames are ignored.

## Connect clients

Create an endpoint on your app, then connect clients to
`wss://<your-app-host>/__gregale/realtime/<endpoint-id>`. The API generates the
endpoint ID. Pipe secrets on stdin so they stay out of shell history:

```bash
printf '%s\n' "$CALLBACK_TOKEN" | gregale realtime create my-app \
  --callback-url https://my-app.example.com \
  --callback-auth-token-stdin \
  --auth-mode oidc_jwt \
  --auth-issuer https://id.example.com \
  --auth-jwks-url https://id.example.com/.well-known/jwks.json \
  --auth-audience realtime \
  --auth-algorithm RS256 \
  --allowed-origin https://my-app.example.com

gregale realtime update my-app ENDPOINT_ID --max-connections 500
gregale realtime delete my-app ENDPOINT_ID --yes
```

Disabling or deleting an endpoint refuses new handshakes and closes its open
connections.

### Authentication

Each endpoint uses one client authentication mode:

- `none`: anyone who knows the URL may connect. Use it only for public data.
- `static_bearer`: clients send `Authorization: Bearer <token>` with a token
  you set (`--auth-token-stdin`). Suits a few trusted backend clients. Rotate
  it without downtime with `gregale realtime auth rotate`, which keeps the old
  token valid until a grace deadline or `gregale realtime auth finalize`.
- `oidc_jwt`: clients send a JSON Web Token from your identity provider.
  Gregale checks its signature (RS256/384/512 or ES256/384/512) against your
  JWKS URL, the issuer, the audience, any required claims (`--auth-claim
  KEY=VALUE`) and the expiry. The token's `sub` becomes the connection's
  principal. Use it for browsers and multi-tenant apps.

Browsers cannot set the `Authorization` header on a WebSocket, so a browser
sends its token as a second requested subprotocol,
`gregale.realtime.bearer.<token>` (the Node SDK does this for you). This works
only on an endpoint with `allowed_origins`, and the browser's `Origin` must
match one of them exactly. Tokens are at most 3,072 bytes.

### Callbacks

Gregale calls your app, with `Authorization: Bearer <callback token>`, at these
paths under the callback URL:

| Path (default) | When | Your answer |
|---|---|---|
| `/realtime/connect` | a client connects on the original protocol | any 2xx admits it; anything else refuses the handshake |
| `/realtime/message` | a client sends a frame on the original protocol | any 2xx |
| `/realtime/disconnect` | a connection closes | any 2xx |
| `/realtime/authorize-channel` | a resumable client subscribes, or sets presence on a channel | any 2xx grants; 401 or 403 refuses |

Check the callback token on every request. Each callback body is JSON with the
event `type`, `endpoint_id`, `connection_id`, `principal` and, for
`authorize-channel`, the `channel` and the `permission` asked for: `read` to
subscribe, `presence` to set presence state or send signals. A grant lasts 30
seconds and is checked again after that while the subscription stays open; a
401 or 403 then removes the subscription with an `access_revoked` error, and
any other failure of that check closes the connection with code 1013 so the
client reconnects. Gregale retries a callback that times out or answers 408, 429 or
5xx, up to three attempts. Callbacks wake a parked app and keep it running
while subscriptions are active, so grant channels from the token with
[channel rules](#channel-rules) where you can.

To rotate the callback token, accept both tokens in your app, update the
endpoint (`PATCH …/endpoints/{id}` with `callback_auth_token`), and keep
accepting the old one for a few minutes while queued callbacks drain.

### Original protocol

A client that requests no subprotocol uses the original protocol: your backend
manages its subscriptions, and messages arrive as plain text or binary frames.

```bash
gregale realtime connections my-app EP_ID --principal user-123   # find a user's connections
gregale realtime subscribe my-app EP_ID CONNECTION_ID orders      # subscribe one to a channel
gregale realtime publish my-app EP_ID orders --data '{"id":42}'  # every subscriber gets the frame
gregale realtime send my-app EP_ID CONNECTION_ID --data 'hi'     # one connection only
gregale realtime close my-app EP_ID CONNECTION_ID --reason "signed out"
```

These messages are live only: a client that is not connected misses them. A
message is at most 1 MiB. The API equivalents are under
`/v1/apps/{app}/realtime/endpoints/{id}/` (`connections`,
`connections/{connection_id}/subscriptions/{channel}`,
`channels/{channel}/publish`, `connections/{connection_id}/send` and
`connections/{connection_id}/close`).

### Resumable protocol

The resumable protocol (preview) lets a client subscribe itself and catch up on
what it missed. It needs an `oidc_jwt` endpoint (or a
[subscription ticket](#subscription-tickets)) and your app's
`/realtime/authorize-channel` callback or channel rules. A client requests the
`gregale.realtime.v2` subprotocol and sends JSON text frames:

```json
{"type":"subscribe","channel":"updates","after":812}
{"type":"ack","channel":"updates","sequence":820}
{"type":"unsubscribe","channel":"updates"}
```

Gregale answers `subscribed`, then `message` frames in order, each with
`channel`, `sequence`, `message_id`, `data_base64` and `binary`. The client
saves the sequence of the last message it processed and sends it as `after`
when it reconnects; `"after": 0` replays everything still retained. Delivery is
at least once, so deduplicate by `message_id` or `sequence`.

- Only retained messages reach resumable subscribers: publish with
  `--delivery retained` (`?delivery=retained` on the API). Retained messages are
  kept for 24 hours, up to 1,024 per channel, and may arrive up to 5 seconds
  after the publish.
- If a client's `after` is older than the retained history, it gets
  `resync_required` with `oldest_sequence` and `latest_sequence` and is not
  subscribed. Rebuild its state from your own data, then subscribe again from a
  sequence inside that range. The Node SDK's `onResync` callback does this.
- To let Gregale keep the cursor instead, add a stable `subscription` name per
  device or worker; acks then move a cursor Gregale stores (at most 256 per
  endpoint, kept 30 days after last use).
- A connection holds at most 8 channels, and each frame from the client is at
  most 4 KiB. Refusals arrive as `{"type":"error","code":...}` frames with the
  channel, for example `not_authorized` or `subscription_limit`; the
  connection stays open.
- Code 1008 closes a connection whose token expired or changed: reconnect with
  a fresh token. Code 1013 means try again: reconnect from the saved cursor.

## Declare endpoints in gregale.yaml

Name each endpoint with a `slug` and declare it next to your app, so every
deploy creates it, keeps it in step with the file, and can recreate it in
another account. This is a preview: until it is enabled for your account, the
API refuses `slug` and `channel_rules` and the CLI treats `realtime:` as an
unknown key.

```yaml
realtime:
  endpoints:
    - slug: chat
      auth: none                   # or an oidc: block (issuer, jwks_url, audience, algorithms)
      allowed_origins: [https://shop.example.com]
      max_connections: 500
      callbacks:                   # paths on this app; these are the defaults
        connect: /realtime/connect
        message: /realtime/message
        disconnect: /realtime/disconnect
      consumer_groups:
        - channel: orders
          name: billing
          lease_seconds: 120
```

- The first deploy creates the endpoint and stores a callback token in the app
  as `REALTIME_CHAT_CALLBACK_TOKEN` (the slug in upper case). Check it on every
  callback.
- Later deploys update changed settings. An endpoint you remove from the file is
  disabled, not deleted, so its history and consumer groups survive; delete it
  with `gregale realtime delete` when you are sure.
- Consumer groups are created when missing and never deleted by a deploy.
- Endpoints created without a slug (API, CLI, dashboard) are never touched. Give
  one a slug with `PATCH …/endpoints/{id}` `{"slug": "chat"}` to manage it from
  the file.
- A failed deploy rolls all of it back.

### Channel rules

An endpoint whose clients sign in with tokens (`oidc`, or Gregale Auth) can
grant channels from the token itself, with no authorize callback, so a
subscription does not wake your app:

```yaml
    - slug: rooms
      oidc: {issuer: "https://id.example.com", jwks_url: "https://id.example.com/jwks.json", algorithms: [RS256]}
      channels:
        - pattern: "org:{org_id}:room:*"   # {org_id} must equal the token's org_id claim
          read: true
          presence: true                   # members may also set presence state and send signals
        - pattern: "user:{sub}"            # sub is the token's subject
          read: true
```

A pattern is literal text, `{claim}` placeholders and an optional trailing `*`.
A channel that matches a rule is granted for as long as the token is valid; any
other channel goes to your authorize callback as before. `read` lets a client
subscribe and appear in presence; `presence: true` also lets it set presence
state and send signals. Without it, presence state and signals on that channel
go to your callback with `permission: "presence"`.
Use claims your identity provider sets, never ones users can edit. Revoking a
user takes effect when their token expires, so keep channels that need faster
revocation on the callback. Activity-scope channels always use the callback.
The same rules are `channel_rules` on `PATCH …/endpoints/{id}`; an endpoint
holds up to 32.

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
- **Resumable subscriptions** (v2 WebSocket channels, the inbox and SSE
  streams) each read their channel's history from the control plane, so an
  account holds at most 16 (Hobby), 64 (Pro) or 128 (Scale) of them on one
  realtime node, and a connection at most 8. A refused subscription gets
  `subscription_limit` (HTTP 429 for SSE); new connections may land on another
  node.
- **Retained history** keeps a channel's messages for 24 hours, at most 1,024
  of them of up to 4 KiB each. A channel counts while it holds retained
  messages, reducer state or a snapshot: an endpoint holds at most 32 such
  channels at a time, and an account 32 (Hobby), 256 (Pro) or 1,024 (Scale).
  A channel frees its slot when its messages expire. A publish that would
  retain on one more channel gets HTTP 409 naming the limit and the number
  held.
- **Per-user state:** an endpoint keeps inboxes for at most 256 users with
  unexpired messages at a time and at most 1,024 read markers updated in the
  last 30 days. An account keeps notification preferences for 1,000 (Hobby),
  10,000 (Pro) or 50,000 (Scale) users; setting a user's preferences back to
  the defaults frees the slot. These refusals are HTTP 429 with code
  `capacity_unavailable`.
- **Event schemas:** 64 versions per channel and event type, and 1,024 per
  endpoint.
- **Functions triggered by channel messages** bill as normal compute.

An endpoint's `max_connections` still caps that endpoint; the account limit
applies on top of it.

See where you stand with `gregale realtime usage` (or `GET /v1/usage/realtime`,
a preview that answers 404 until it is enabled for your account): connections open
now against your limit, and the peak connections, connection-hours and messages
for each recent hour, per account and per endpoint.

## Subscription tickets

A subscription ticket (preview) lets your backend hand a browser or app a
short-lived credential for named channels without an OIDC provider. Your
backend is the authority: sign the user in with your own login, decide which
channels they may read, then issue the ticket with your Gregale API key:

```http
POST /v1/apps/my-app/realtime/endpoints/{endpoint_id}/tickets
Authorization: Bearer <your API key>
Content-Type: application/json

{"principal":"customer-42","channels":["orders:tenant-42"],"ttl_seconds":120}
```

It answers 201 with `{"ticket": "grt_…", "ticket_id": "…", "expires_at": "…"}`.
A ticket lives 1 to 300 seconds and names 1 to 32 exact channels; an endpoint
holds at most 1,024 unexpired tickets. Never put your API key in the browser,
and never pass a principal or channel list the user chose without checking it.

The client sends the ticket like a token: `Authorization: Bearer <ticket>`, or
the browser subprotocol (`createBrowserRealtimeTicketSocketFactory` in the Node
SDK fetches a fresh ticket on every reconnect), never in a query string. A
ticket can be reused until it expires, and an expired ticket closes the socket.
Ticket connections are read-only: they may subscribe, unsubscribe and ack, but
not set presence, send signals or use named subscriptions (`ticket_read_only`),
and they trigger no connect or authorize callbacks.

Store the `ticket_id`, not the ticket. Revoke one ticket with
`DELETE …/tickets/{ticket_id}` (204, or 404 once it has expired), or every
ticket of a user at logout with `DELETE …/tickets?principal=…` (`{"revoked":
n}`). Connections using a revoked ticket close within about a second, and
reconnecting with it gets 401.

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

Setting presence state needs the `presence` permission: a channel rule with
`presence: true`, or your authorize callback granting `permission: "presence"`.
Without it, a subscriber joins with empty state, and a state change gets a
`presence_not_authorized` error frame while the subscription stays open.

Signals are transient messages between subscribers, such as typing
indicators. They need the same permission, carry JSON data up to 2 KiB, never
enter history, and are limited to 20 a second per connection and 50 a second
per channel:

```json
{"type":"signal","channel":"room-a","data":{"kind":"typing","active":true}}
```

With the Node SDK, pass `presence` (initial state), `presenceScope` and
`onPresence` to `consumeRealtimeChannel`, and update state through the actions
passed to `onSubscribed` (`updatePresence`).

## Channel occupancy

Occupancy tells you how many clients are on a channel right now, for "23
watching" badges, room sizes or dashboards. It is a preview: until it is
enabled for your account, the occupancy API answers 404 and subscriptions get
no counts. Ask for up to 20 channels at once:

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
too. An SSE stream adds `occupancy=1` to its URL (or passes `onOccupancy` to
`subscribeRealtimeSSE`) and gets `event: occupancy` with the same data. If a realtime node
already follows its maximum of 1,000 live-count channels, the subscription
still works and the client gets an `occupancy_limit` error frame instead.

## Browser apps and React

`createRealtimeClient` from the Node SDK runs in the browser. It keeps one
WebSocket per endpoint, subscribes a channel while something on the page
watches it, and gives each channel a view with its status, recent messages,
presence and occupancy. React hooks come from `@gregale/sdk-node/react`
(React 18 or later):

```tsx
import { createBrowserRealtimeTicketSocketFactory, createRealtimeClient } from '@gregale/sdk-node/preview/browser';
import { RealtimeProvider, useOccupancy, usePresence, useRealtimeChannel } from '@gregale/sdk-node/preview/react';

const client = createRealtimeClient({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  // Your backend issues a subscription ticket for the signed-in user.
  webSocketFactory: createBrowserRealtimeTicketSocketFactory(() => fetch('/realtime-ticket').then((r) => r.text())),
});

function Room({ id }: { id: string }) {
  const { status, messages } = useRealtimeChannel(`room-${id}`, { presence: { name: 'Ada' } });
  const people = usePresence(`room-${id}`);
  const watching = useOccupancy(`room-${id}`);
  // status: idle, connecting, live, reconnecting, error or closed
}

<RealtimeProvider client={client}><Room id="42" /></RealtimeProvider>
```

- A channel is subscribed when the first component watches it and released
  about a second after the last one unmounts, so re-renders and quick
  navigation keep the subscription. The WebSocket closes when nothing is
  watched.
- A view keeps the 50 most recent messages (`messageLimit`, up to 1,000). A
  reconnect or a re-watched channel resumes from the last message received.
- A refused channel (for example `not_authorized`) shows `status: "error"` with
  the code; the client's other channels keep working.
- One client watches at most 8 channels at once, the limit of one connection.
- Watch options (presence state, filter, occupancy) apply when a channel is
  first watched; `updatePresence` on the hook result changes presence later.

Outside React, `client.channel(name).subscribe(listener)` and `getView()` give
the same view to any UI library. These features use the
[resumable protocol](#resumable-protocol).

## Principal access controls preview

Access controls (preview) cut a user off without finding their sockets, for
example when they leave a workspace or sign out everywhere. They work on
`oidc_jwt` endpoints and on subscription tickets; static bearer and
unauthenticated endpoints have no per-user principal to revoke.

```http
POST /v1/apps/my-app/realtime/endpoints/{endpoint_id}/principals/user-123/access
Authorization: Bearer <your API key>
Content-Type: application/json

{"action": "revoke_channel", "channel": "project-42"}
```

- `revoke_channel` removes the user's subscriptions to that channel (and its
  activity scopes) and refuses new ones; their clients get an `access_revoked`
  error frame. Frames already sent cannot be recalled.
- `restore_channel` lifts the deny. Your authorize callback must still grant
  the channel, and the client must subscribe again.
- `revoke_sessions` closes the user's connections whose token was issued (`iat`)
  at or before now, and refuses those tokens on reconnect. A token issued
  afterwards connects again, so a permanent ban belongs in your sign-in. With
  access controls on, tokens need an `iat` claim.

`GET` on the same path returns `denied_channels`, `sessions_revoked_before`,
`revision` (0 means no controls) and `updated_at`. Changes reach every node
within about a second. An endpoint keeps controls for at most 1,024 users and
64 denied channels per user; they stay until the endpoint is deleted. The Node
SDK has `revokeRealtimeChannelAccess`, `restoreRealtimeChannelAccess` and
`revokeRealtimeSessions`.

## Function triggers

A realtime trigger runs one of your functions for each message on a channel, so
you do not operate always-on workers. It consumes an existing consumer
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
      endpoint: chat              # an endpoint declared under realtime.endpoints,
                                  # or endpoint_id: <UUID> for any other endpoint
      channel: orders
      group: billing
      max_concurrency: 4          # optional; defaults to 1
```

Deploy matches the app's realtime triggers to the file by `slug` and rolls the
change back if the deploy fails.

Your function receives a JSON body with `endpoint_id`, `channel`, `group`,
`sequence`, `message_id`, `event`, `version`, `deleted`, `data_base64`,
`binary`, `metadata`, `created_at`, `attempt` and `fencing_token`, and returns
an empty or JSON body:

| Result | What happens to the message |
|---|---|
| 2xx | completed |
| the function fails, times out or returns a malformed response | retried under the group's retry policy; a permanent failure code dead-letters it at once |
| Gregale could not invoke it (wake limit, transport failure) | returned to the group without spending an attempt |

A trigger runs up to `max_concurrency` messages at once (capped by your plan's
concurrency and 32), and messages sharing an ordering key still run one at a
time. Dead letters, replay, export, pause and drain stay on the group; pausing
or draining it stops the trigger.

### Several messages per invocation

On a busy channel, set `delivery: batch` (with an optional `batch_size_max` up
to 16, default 10) so one invocation handles several messages, which costs
less compute than one invocation each. Batch delivery is a preview: until it is
enabled for your account, requests for it get HTTP 422. The same option works for
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

The SDKs do the parsing and bookkeeping. A handler that throws for a record
puts just that record in `batchItemFailures`:

```ts
import { handleTriggerBatch } from '@gregale/sdk-node/preview/triggers/runtime';

export async function POST(request: Request) {
  const result = await handleTriggerBatch(await request.json(), async (record) => {
    await saveOrder(record.data);            // record.bytes / record.text() for non-JSON
  });
  return Response.json(result);
}
```

```python
from faas_sdk.preview.trigger_batch import handle_trigger_batch

def handler(request):
    return handle_trigger_batch(request.body, lambda record: save_order(record.data))
```

Records run one at a time in delivery order. Pass `concurrency` (Node, or
`handle_trigger_batch_async` in Python) when records do not depend on each
other's order. `stopOnFailure` / `stop_on_failure` reports every record after a
failure for retry without running it, keeping order at the cost of extra
attempts for those records. A realtime trigger's batch never holds two
messages with the same ordering key, so `concurrency` is safe there.

```yaml
triggers:
  - kind: realtime
    app: notify
    slug: orders
    delivery: batch
    batch_size_max: 10
    config: {endpoint_id: 6f1c2a4e-1d9b-4c7a-9a39-0b8d6f0f4c11, channel: orders, group: billing}
```

`gregale trigger add --delivery batch` and `PATCH /v1/triggers/{id}` with
`{"delivery": "batch"}` set it outside a manifest.
