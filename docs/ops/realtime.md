# Managed realtime operations

`faas-realtimed.service` owns opt-in managed WebSocket connections. It listens
on `/run/faas/realtimed.sock`; `gatewayd-internal` forwards the reserved
`/__gregale/realtime/` namespace there when
`FAAS_REALTIME_SOCKET=/run/faas/realtimed.sock` is set.
Its loopback health listener (by default `127.0.0.1:9107`) exposes only
`/healthz` and `/readyz`; management routes remain available only on the
DAC-protected Unix socket.

The service is intentionally separate from the raw application WebSocket
bridge. Restarting it closes managed connections and emits disconnect events;
application-owned raw Upgrade sessions are unaffected.

Disabling or deleting an endpoint stops new handshakes and closes its existing
managed sockets on every node reached by the control-plane operation. The
control plane compares the credential-free registration inventory on active
nodes with durable endpoint rows every 30 seconds, so a node that missed a
delete is cleaned up after it becomes reachable again. A delete response
records customer intent; operators should check node reachability when
immediate fleet-wide revocation matters.

Register an endpoint (normally from an authorized control-plane process), then
connect clients to `wss://<app-host>/__gregale/realtime/<endpoint-id>`:

Customer applications should use the authenticated API resource instead of
writing the daemon socket directly:

```
POST /v1/apps/{slug}/realtime/endpoints
GET|PATCH|DELETE /v1/apps/{slug}/realtime/endpoints/{id}
POST /v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate
POST /v1/apps/{slug}/realtime/endpoints/{id}/auth/rotate/finalize
GET /v1/apps/{slug}/realtime/endpoints/{id}/connections
POST /v1/apps/{slug}/realtime/endpoints/{id}/connections/drain
GET /v1/apps/{slug}/realtime/endpoints/{id}/connections/drain/{drain_id}
POST /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/send
POST /v1/apps/{slug}/realtime/endpoints/{id}/principals:send
GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/messages/{message_id}/receipt
GET /v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox
POST /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/close
PUT|DELETE /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}
POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish
```

The CLI can create and update endpoint policy without exposing credentials in
the process list. The API generates the endpoint ID:

```sh
printf 'callback-secret\n' | \
  gregale realtime create my-app \
    --callback-url https://app.example.com/realtime/events \
    --callback-auth-token-stdin \
    --auth-mode oidc_jwt \
    --auth-issuer https://issuer.example.com \
    --auth-jwks-url https://issuer.example.com/.well-known/jwks.json \
    --auth-audience realtime \
    --auth-algorithm RS256 \
    --allowed-origin https://app.example.com

gregale realtime update my-app ENDPOINT_ID --max-connections 500 --enable
gregale realtime delete my-app ENDPOINT_ID --yes
```

To safely drain every matching connection, use explicit all mode. It is capped
at 10,000 connections, cannot be combined with `--limit` or `--connection-id`,
and refuses to start when the inventory exceeds the cap. The existing partial
fleet guard still applies:

```sh
gregale realtime drain my-app ENDPOINT_ID \
  --principal user-123 \
  --all \
  --reason "user session migration" \
  --dry-run
```

Use `--auth-token-stdin` when configuring `static_bearer`. For an update,
`--auth-mode none` removes the existing client credential and OIDC policy;
`--clear-allowed-origins` removes the browser-origin restriction. Repeated
`--auth-audience`, `--auth-algorithm`, `--auth-claim KEY=VALUE`, and
`--allowed-origin` flags replace their respective lists.

The API persists endpoint configuration in the control plane, applies the
per-plan inventory cap, returns masked credentials, and mirrors enabled rows to
active realtime nodes. In a single-box install this is the local
`FAAS_REALTIME_SOCKET`; in a multi-node install apid uses each node's private
`gateway_target_url` and the `gatewayd-internal` control proxy. Connection
operations are routed through the leased owner directory, while publish is
broadcast to active nodes by default. New apid versions also record shared
PostgreSQL channel-to-node hints while routing is disabled. After every apid
replica has been upgraded, set
`FAAS_REALTIME_CHANNEL_ROUTING_ENABLED=1` on all replicas to publish only to
nodes with subscribers. Each apid seeds readiness from node-local snapshots of
the endpoint/channel subscriber index, including legacy and resumable
subscriptions; during rolling upgrades it falls back to the per-connection
inventory when an older realtime node lacks the compact snapshot endpoint. A
new or unready node stays in the publish set until its snapshot succeeds.
Legacy subscribe/unsubscribe operations update route hints synchronously.
After channel authorization, realtimed reports resumable route transitions to
apid over the existing private history connection, so channel targeting can
update before the subscription acknowledgement. Failed reports are logged and
repaired when apid polls the process-scoped route revision on its 30-second
reconcile pass. That pass refreshes a node snapshot when a route appears or
disappears or the realtime process restarts. The last applied process and
revision are stored with the shared node snapshot so any apid replica can
continue from the same checkpoint. Ready snapshots still refresh every five
minutes as a recovery path for older nodes and missed revisions.
Directory read errors fall back to full broadcast. The route directory caps each
endpoint at 10,000 rows by isolating the channels with the largest route sets;
after node snapshots are ready, those channels use full broadcast while
unrelated indexed channels remain targeted. The reconciler retries isolated
channels every five minutes. A missed resumable route wake can be recovered by
the subscriber's bounded history poll while the message remains retained. The
daemon-socket example below remains useful for node-local bootstrap and
recovery tooling.

When channel routing is enabled, each apid keeps a bounded cache of publish
targets while its PostgreSQL route-change listener is connected. Committed
route changes invalidate their endpoint/channel entry; endpoint overflow
changes invalidate that endpoint's entries. Snapshot generation, readiness,
and active-node changes invalidate the full cache. If the listener falls
behind, it collapses pending notifications into a full invalidation. The cache
is cleared and disabled when the listener disconnects, then enabled with an
empty cache after it reconnects. Cold lookups continue through the shared route
directory.

Apid exports bounded-cardinality route-directory metrics in its operations
registry. `apid_realtime_channel_route_publish_decisions_total` counts
routing decisions as `routing_disabled`, `route_store_unavailable`,
`directory_error`, `overflow`, `unready_fallback`, `targeted`, or
`no_subscribers`. `apid_realtime_channel_route_publish_recipients` records the
number of active nodes selected for each decision, so targeted fanout can be
compared with fallback broadcasts. Rebuild health is reported by
`apid_realtime_channel_route_rebuild_checks_total` (`started`, `idle`, `error`,
`canceled`), `apid_realtime_channel_route_reconcile_passes_total`
(`complete`, `incomplete`, `error`, `canceled`), and
`apid_realtime_channel_route_node_snapshots_total` (`success`, `error`,
`canceled`). Revision polling is reported by
`apid_realtime_channel_route_revision_polls_total` (`unchanged`,
`refresh_required`, `unsupported`, `error`, `canceled`).
`apid_realtime_channel_route_node_snapshot_sources_total` records snapshot
attempts by source (`rebuild`, `periodic`, `revision`) and outcome. The
`apid_realtime_channel_route_target_cache_lookups_total` counter reports
target-cache `hit`, `miss`, `disabled`, and `coalesced` lookups. Coalesced
lookups shared an in-flight directory query or used the cache after waiting
for another lookup. The
`apid_realtime_channel_route_reconcile_duration_seconds` histogram records the
duration of passes that start. These metrics omit endpoint, channel, and node
identifiers.

```promql
sum by (decision) (rate(apid_realtime_channel_route_publish_decisions_total[5m]))
histogram_quantile(0.95, sum by (decision, le) (rate(apid_realtime_channel_route_publish_recipients_bucket[5m])))
sum by (outcome) (rate(apid_realtime_channel_route_rebuild_checks_total[15m]))
sum by (outcome) (rate(apid_realtime_channel_route_reconcile_passes_total[15m]))
sum by (outcome) (rate(apid_realtime_channel_route_revision_polls_total[15m]))
sum by (source, outcome) (rate(apid_realtime_channel_route_node_snapshot_sources_total[15m]))
sum by (outcome) (rate(apid_realtime_channel_route_target_cache_lookups_total[5m]))
histogram_quantile(0.95, sum by (le) (rate(apid_realtime_channel_route_reconcile_duration_seconds_bucket[15m])))
```

Warnings for sustained publish fallbacks and unsuccessful overflow rebuilds,
with recovery steps, are documented in the
[managed realtime channel routing runbook](../runbooks/FaasManagedRealtimeChannelRouting.md).

## Zero-downtime static bearer rotation

Static bearer credentials are rotated without disconnecting clients. The
replacement becomes current immediately; the old credential remains accepted
for the requested grace period (5 minutes by default, at most 24 hours).
Responses expose only the predecessor expiry timestamp—never either token.

```sh
# Read the replacement from a secret manager or protected pipe.
secret-manager read realtime/new-token | \
  gregale realtime auth rotate my-app ENDPOINT_ID --token-stdin --grace-period 900

# Inspect the safe status later; this never prints token material.
gregale realtime auth status my-app ENDPOINT_ID

# Revoke the predecessor before its deadline when every client has migrated.
gregale realtime auth finalize my-app ENDPOINT_ID
```

Use `--token TOKEN` only for compatibility with automation that cannot pipe
stdin; it is visible in shell history and process inspection. After the grace
deadline the predecessor is rejected even if finalization has not been called.
An explicit finalize is useful when migration completes early or when a
credential may have been exposed.

Realtime v2 endpoint policies can add `allowed_origins`,
`max_connections`, `max_message_bytes`, and `max_connection_age_seconds` to
the endpoint resource. Origins are exact `http://` or `https://` origins (up
to 16 entries); wildcards are intentionally not supported. A zero numeric
value inherits the node-wide `FAAS_REALTIME_*` default, and endpoint values
cannot exceed the daemon's global cap. Empty `allowed_origins` preserves the
legacy non-browser/client behavior; a non-empty list is enforced during the
WebSocket handshake.

Endpoint writes are best-effort fan-out operations. apid performs an immediate
reconciliation at boot and every 30 seconds, replaying enabled rows and
removing disabled rows on active nodes. If a node is restarting or unreachable,
the pass records the failure and retries on the next interval; no endpoint
mutation is required to heal the node after it becomes active.

Client authentication is configured per endpoint. Use `auth_mode: none` for a
public endpoint, `static_bearer` with the write-only `auth_token` for a small
controlled integration, or `oidc_jwt` with `auth_issuer`, `auth_jwks_url`,
`auth_audience`, `auth_algorithms`, and optional `auth_required_claims` for
browser or multi-tenant clients. JWTs must be signed with RS256/384/512 or
ES256/384/512; the verified `sub` claim becomes the callback `principal`.
JWKS fetches use the daemon's bounded cache and fail closed when the policy
cannot be verified. The API never returns client tokens; metadata is returned
for inspection and credentials remain masked.

The connection-owner directory is also swept independently: expired leases
left by crashed processes are deleted in batches at boot and every minute.
Active leases are preserved, and a cleanup failure is retried automatically.

```
curl --unix-socket /run/faas/realtimed.sock -X POST http://localhost/internal/endpoints \
  -H 'content-type: application/json' \
  -d '{"id":"notifications","app_id":"app-123","account_id":"acct-123","callback_url":"https://notifications.example.com","connect_path":"/_gregale/realtime/connect","message_path":"/_gregale/realtime/message","disconnect_path":"/_gregale/realtime/disconnect","callback_auth_token":"optional-callback-bearer"}'
```

The callback URL should be an ordinary application route. Its first request
wakes a sleeping VM; the quiet WebSocket itself remains owned by `realtimed`.
Updating `callback_auth_token` with the endpoint PATCH applies the new bearer
to future callbacks from existing connections without closing their sockets.
Callbacks already persisted in the durable outbox keep the token captured when
they were queued. During rotation, configure the handler to accept both tokens,
update Gregale's endpoint, and keep accepting the old token until pending
callbacks have drained. Review dead letters before revoking the old token if
you may need to replay them manually.
Use the authenticated API (or `pkg/realtime.Client` for node-local tooling) to
send to a `connection_id`, subscribe/publish channels, or close a connection.
Send and publish bodies contain `data_base64` and an optional `binary` flag;
decoded frames are limited to 1 MiB. The built-in HTTP callback hook retries
transient network failures and 408/429/5xx responses up to three total attempts
with a bounded backoff before reporting a callback error. In multi-node mode, apid discovers and
leases the connection owner, renews the lease for the operation, and retries a
stale owner once. Endpoint registration must be able to reach each node's
private `gateway_target_url`; missing or unreachable nodes remain fail-closed
for connection operations (`503`). The publish response reports `subscribers`,
`queued`, `queue_full`, and `failed` across reachable nodes. For live delivery,
`queued` means admission to an in-memory output queue; retained delivery also
counts accepted resume-worker wake-ups. Neither means client acknowledgement.
`partial` is true when a node or target subscriber could not accept the
publish. Retrying a partial publish without an idempotency key may duplicate
messages already queued elsewhere. Supply a stable `Idempotency-Key` to bind
retries to the delivery mode, decoded payload, and binary flag for 24 hours. The same key
replays the original queue outcome without retrying recipients that missed a
partial publish, while reuse with a different mode or payload returns `409`. An
in-flight or uncertain reservation also returns `409` and is not run again while
the key is active. If
the original outcome could not be recorded, that reservation expires after 24
hours; retrying then may publish again and could duplicate a message accepted
by the first attempt. Owner failures are replayed too when their delivery
result may be ambiguous. Queue admission still does not confirm client receipt.

For a live-only notification to all connected devices for one authenticated
user, use `POST /v1/apps/{slug}/realtime/endpoints/{id}/principals:send` with
`principal`, `data_base64`, and optional `binary`. Principal delivery requires
an endpoint configured for `oidc_jwt`; the principal must be the exact verified
OIDC `sub`. It targets every active connection for that endpoint and queues the
message directly, independent of channel subscriptions. It is not retained for
offline users and queue admission does not confirm receipt. Payloads are
limited to 4 KiB so they fit the v2 control frame. Legacy clients receive a raw
WebSocket frame. V2 clients receive a `direct_message` frame when they opt in;
older v2 clients stay connected and are counted as `unsupported` in the
response.
The response reports matched recipients, queued messages, unsupported clients,
queue failures, and node availability. Queue admission still does not confirm
client receipt.

For per-connection delivery receipts, include `request_receipt: true` and a
caller-supplied stable `message_id`. Each matched connection is queued at most
once during the one-hour receipt window; reusing the ID with a different
principal or payload returns a conflict. The Node SDK acknowledges after the
`onDirectMessage` callback resolves. Make that callback finish the application
side effect before it resolves. V2 clients must opt in with `onDirectMessage`;
legacy clients can still receive a raw frame but cannot acknowledge it. An
acknowledgement not received within 30 seconds is reported as `timed_out`.
Receipt-enabled sends require the API and realtime nodes to be upgraded
together; ordinary principal sends remain compatible during a rolling upgrade.
Receipt-enabled delivery also requires the resumable v2 preview on realtimed:
`FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1`.

```sh
MESSAGE_ID="job-847-progress-1"
gregale realtime send-principal my-app ENDPOINT_ID \
  --principal user-123 --message-id "$MESSAGE_ID" --receipt \
  --data '{"job_id":"job-847","percent":75}'

gregale realtime receipt my-app ENDPOINT_ID "$MESSAGE_ID"
```

The receipt lookup lists each matched connection ID and its queue and
acknowledgement status. Receipt records expire after one hour. They cover
active connections only and do not replay messages after reconnect.

```sh
curl -X POST "${GREGALE_API}/v1/apps/my-app/realtime/endpoints/ENDPOINT_ID/principals:send" \
  -H "Authorization: Bearer ${GREGALE_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"principal":"user-123","message_id":"job-847-progress-1","request_receipt":true,"data_base64":"eyJldmVudCI6Im5vdGlmaWNhdGlvbiJ9"}'

curl "${GREGALE_API}/v1/apps/my-app/realtime/endpoints/ENDPOINT_ID/principals/messages/job-847-progress-1/receipt" \
  -H "Authorization: Bearer ${GREGALE_TOKEN}"
```

For offline principal delivery, set `delivery: "retained"` and supply a stable
`message_id` on the same `principals:send` API. The API commits an ordered inbox
message and returns `durable: true` with its `sequence`, including when every
device is offline. Active realtime nodes receive a wake after the commit.
Polling the committed inbox (five seconds by default) recovers a lost wake.
This path requires an enabled
`oidc_jwt` endpoint, `FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1` on apid, and
`FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1` on realtimed. Apply the principal inbox
migration and upgrade apid and realtimed before opting clients into this path.

```sh
gregale realtime send-principal my-app ENDPOINT_ID \
  --principal user-123 --delivery retained --message-id job-847-complete \
  --data '{"job_id":"job-847","status":"complete"}'

# Inspect retained messages. Add --json to include the base64 payloads.
gregale realtime inbox my-app ENDPOINT_ID --principal user-123 --after 0

# Inspect the named device checkpoint and messages after that checkpoint.
gregale realtime inbox my-app ENDPOINT_ID --principal user-123 --consumer phone-1
```

Clients use the v2 `inbox_subscribe` frame with a stable `consumer` device name.
The server derives the inbox identity from the verified OIDC principal; the
client supplies no principal or channel. The SDK's `consumeRealtimeInbox`
helper subscribes, replays ordered `inbox_message` frames, and sends cumulative
`inbox_ack` frames after its message callback resolves. A checkpoint is committed
before `inbox_acknowledged` is returned. Use different consumer names for devices
that need independent progress. Devices sharing a consumer name share a
checkpoint. A lost acknowledgement can replay a message, so application side
effects must be idempotent using the message ID or inbox sequence scoped to
the endpoint and authenticated principal.

Inbox storage is separate from channel history. Initial preview limits are
256 principal streams per endpoint, 256 messages per principal, 4 KiB per
message, 16 device checkpoints per principal, and 1,024 device checkpoints per
endpoint. Messages expire after 24 hours and can be removed sooner when the count limit is reached. Inbox
sequence heads remain after payload expiry to preserve ordering, so principal
stream slots are retained for the endpoint's lifetime. Inactive device
checkpoints expire after 30 days. Endpoint deletion removes its inbox records.
Existing channel-history usage reports exclude this separate inbox storage.

Message IDs deduplicate within one endpoint and principal while the message is
retained. Repeating an ID with the same bytes and binary flag returns its saved
sequence; different bytes or a different binary flag return a conflict. IDs can
be used again after their messages leave retained history. Retained sends use
device checkpoints, and cannot combine `delivery: "retained"` with
`request_receipt: true`. A device acknowledgement advances only that device's
checkpoint; other devices and newly enrolled devices can still replay the
retained message.

An inbox cursor older than retained history produces `inbox_resync_required`.
The SDK invokes its inbox `onResync` callback, or throws
`RealtimeInboxResyncRequiredError` when no callback is supplied. Rebuild
application state, then return a checkpoint between `oldestSequence - 1` and
`latestSequence`; the SDK submits `inbox_reset` and reconnects. This recovery
is explicit so missed notifications are never silently skipped.

The management inbox lookup accepts `principal`, optional `consumer`, optional
`after`, and a `limit` between 1 and 100. Omitting `after` starts at the specified
device's acknowledgement, or zero when no consumer is supplied. The response
includes `acknowledged_sequence` when inspecting a device, retained bounds,
`history_unavailable`, and `has_more`. A missing or expired device checkpoint
returns 404. The default CLI output prints IDs and checkpoints; JSON output also
includes payloads. Reading the management API never advances a checkpoint.

The retained-message management API and resumable publish path are an early
preview. They are disabled by default; set
`FAAS_REALTIME_RETAINED_PREVIEW_ENABLED=1` on apid and
`FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1` on realtimed to exercise live resume
delivery in a controlled environment. This feature has no finalized plan
entitlement or storage pricing. A normal `:publish` stays live-only. Add
`?delivery=retained` to that publish route to commit the message before live
fan-out; retained publishes require an `Idempotency-Key`, accept at most 4 KiB,
and return `durable: true` with the channel `sequence`. A best-effort wake
promptly advances v2 subscribers from their last sent sequence; bounded history
polling recovers if a wake is missed. Legacy raw-frame subscribers still receive
the usual live message. Retained publishes use the channel target index to
reach nodes holding legacy or resumable subscribers. Route lookup errors and
overflow use fleet-wide fallback, while nodes with unready snapshots remain in
the target set. If a direct route report fails, the 30-second revision pass
repairs the index; bounded history polling can catch up while the message
remains retained if that delays a wake. If live fan-out is partial, the
committed sequence is still authoritative and resumable clients can catch up
from history.

`POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages`
remains an append-only storage operation. `GET` on the same path with
`after=<last sequence>` returns a page and the current retention bounds; an
expired cursor returns `410 history_unavailable` so a caller can rebuild its
state. The storage window is capped at 1,024 messages of 4 KiB each per channel
and 32 channels per endpoint. Messages remain available for up to 24 hours.
The publish API's idempotency response is retained for 24 hours; append-only
message keys deduplicate only while their messages remain in history. Do not
use this preview as a production reconnect contract until plan entitlements,
billing rules, and fleet qualification are complete.

Apid samples the physical PostgreSQL storage allocated to the history head
and message relations after each one-minute expiry pass. The
`apid_realtime_history_relation_bytes{relation="heads|messages"}` gauges include
indexes and space awaiting vacuum. `apid_realtime_history_sample_success` and
`apid_realtime_history_last_sample_timestamp_seconds` identify stale samples;
`apid_realtime_history_pruned_messages_total` and
`apid_realtime_history_prune_failures_total` show cleanup activity. Every apid
replica observes the same database, so use `max by (relation)` for relation
bytes across replicas rather than summing them:

```promql
max by (relation) (apid_realtime_history_relation_bytes)
min(apid_realtime_history_sample_success)
time() - min(apid_realtime_history_last_sample_timestamp_seconds)
sum(rate(apid_realtime_history_pruned_messages_total[5m]))
```

These are physical capacity measurements, not per-account billable usage.

`GET /v1/account/realtime-history-usage` is available with the retained
history preview enabled and the `usage:read` scope. It returns one account's
current channel-head count, message-row count, and decoded payload bytes.
`stored_*` includes expired rows until the reaper removes them;
`replayable_*` applies the same contiguous expiry floor used by subscription
resume. This snapshot excludes row and index overhead and is not a billable
byte-hour meter. Use the global relation metric above to watch actual database
allocation; the account view is for tenant attribution and preview evaluation.

With `DATABASE_URL` pointed at a throwaway PostgreSQL database, run the local
continuity check:

```sh
go test ./pkg/realtime -run '^TestResumePostgresContinuityAcrossOwnersAndRestart$' -count=1
```

It runs separate realtime owners against one retained log and verifies replay
after disconnect and owner restart, delivery of later commits, channel grants,
and `resync_required` for an expired cursor. It does not exercise the deployed
private RPC or real network failures. Before enabling the preview on a fleet,
follow the [two-node resume qualification runbook](../runbooks/FaasRealtimeResumeQualification.md)
through apid's private history reader. It covers reader unavailability,
endpoint revocation, retention pruning, and slow-client recovery. Confirm that
failures close the subscription or return an explicit resynchronization
response without silently skipping a sequence.

The private `RealtimeHistory.ReadChannelHistory` RPC lets realtimed fetch the
same bounded page from apid. On a single box it shares
`/run/faas/request_telemetry.sock`; split-box apid registers it on the private
AppErrors mTLS listener. It is not exposed by the public gateway.

For a controlled v2 preview, enable `FAAS_REALTIME_RESUME_PREVIEW_ENABLED=1` on
realtimed and point `FAAS_REALTIME_HISTORY_TARGET` at apid's private listener.
Single-box defaults to the Unix socket above. A split-box `tcp://` or `dns://`
target requires `FAAS_REALTIME_HISTORY_TLS_CERT_PATH`, `_KEY_PATH`, and
`_CA_PATH`. The endpoint must use `oidc_jwt` authentication. Its application
must implement `POST <callback_url>/realtime/authorize-channel`: Gregale sends
`realtime.authorize_channel` with the verified principal, endpoint, channel,
and `permission: "read"`, using the configured callback bearer credential.
Any 2xx grants that channel for this connection; all other responses and
callback failures deny it. To revoke a grant already in use, close the
matching connection through the management API.

A v2 client requests the `gregale.realtime.v2` WebSocket subprotocol and sends
JSON text frames:

```json
{"type":"subscribe","channel":"updates","after":812}
{"type":"ack","channel":"updates","sequence":820}
{"type":"unsubscribe","channel":"updates"}
```

Subscriptions may include an optional presence object. The server responds with
`subscribed`, then one or more `presence` frames with `event: "snapshot"`;
`complete: true` marks the final snapshot chunk. Existing subscribers receive
`joined`, `updated`, and `left` presence events. A client changes its state by
sending a `presence` frame. Presence state is a JSON object capped at 512 UTF-8
bytes, and a connection may send at most 10 state updates per second. By
default, each connection has an opaque `member_id`. Set
`presence_scope: "principal"` on the subscribe frame to group connections with
the same verified OIDC principal into one opaque member. The principal remains
private, while `connection_count` reports the number of active sockets in the
group. The most recently changed state wins across devices; use the default
connection scope when each device needs separate state. Principal scope
requires a verified principal and fleet presence storage. Presence state is
client-supplied and is not a verified user identity.

```json
{"type":"subscribe","channel":"room-a","after":0,"state":{"status":"online"}}
{"type":"subscribe","channel":"room-a","after":0,"state":{"status":"online"},"presence_scope":"principal"}
{"type":"presence","channel":"room-a","state":{"status":"typing"}}
```

Clients can send transient channel signals with JSON data up to 2 KiB:

```json
{"type":"signal","channel":"room-a","data":{"kind":"typing","active":true}}
```

Signals are routed to authorized v2 subscribers across active realtime nodes.
They are limited to 20 per second per connection and 50 per second per channel
on each sending node, and never enter retained history or resume cursors.
Presence uses shared 45-second leases renewed every 15 seconds. Join, update,
and leave events are routed across nodes; each node reconciles snapshots every
5 seconds so missed events and expired leases are repaired. A crashed node's
members disappear from active snapshots after their leases expire. Unsubscribe
and graceful socket close remove presence immediately. Presence state remains
client supplied and is not a verified user identity. A channel can hold up to
512 active presence connections; principal-scoped members aggregate those
connections and include the count in snapshots and updates. Signals are best-effort
and a relay failure is reported to the sending client; a presence snapshot
repairs presence event delivery after a transient relay failure. These frames
remain behind the v2 preview flags and the endpoint's channel authorization
callback.

To let Gregale keep the checkpoint, add a stable name for each logical
consumer. Optionally send `after` as that name's first-use baseline:

```json
{"type":"subscribe","channel":"updates","subscription":"phone-install-7","after":812}
{"type":"ack","channel":"updates","sequence":820}
{"type":"reset","channel":"updates","subscription":"phone-install-7","sequence":820}
```

The SDK sends `reset` after `onResync` rebuilds state and waits for the
`subscription_reset` confirmation before reconnecting.

Durable cursor identity is scoped to endpoint, verified principal, subscription
name, and channel. Give each device or independent worker a different name;
connections using the same name share progress. Only clients with a stable,
non-empty authenticated principal can use this mode. Endpoint authentication
and channel authorization still run before the cursor is read. On first use,
`after` seeds the cursor (and can align it with an application snapshot); an
existing named cursor takes precedence. If omitted, a new cursor starts at 0.
If retained history no longer covers that baseline, Gregale requests a resync.

After the channel callback grants access, Gregale returns `subscribed`, then
ordered `message` frames with `channel`, `sequence`, `message_id`,
`data_base64`, and `binary`. An `acknowledged` frame confirms an ack for a
sequence sent on this connection. With client-held cursors, the client persists
its last processed cursor and includes it as `after` on reconnect. With a named
subscription, Gregale stores the cursor after each ack and returns it on the
next subscribe. In either mode, a lost acknowledgement can cause redelivery;
processing is at least once, not exactly once. Deduplicate effects by message
ID or sequence.

An expired cursor returns `resync_required` with `oldest_sequence` and
`latest_sequence` and leaves the channel unsubscribed. The Node SDK can handle
this with an optional `onResync` callback: rebuild application state from a
consistent snapshot or history reader, then return the highest sequence
represented by that state within the reported retention bounds. It saves a
client-held cursor locally, or sends a reset for a named subscription and
waits for Gregale to confirm it before reconnecting. Without the callback, it
raises `RealtimeResyncRequiredError` and leaves the cursor unchanged. The
server retains at most 256 named cursor rows per endpoint and removes cursors
after 30 days without activity. The message history remains bounded to 1,024
messages per channel with a 24-hour retention window, so a cursor can outlive
the messages needed to catch it up and require a resync. While connected,
realtimed polls the
durable log every five seconds; retained writes may therefore arrive with
that delay. The preview caps a node at 256 v2 subscriptions and a connection
at eight. If retention advances past a connected subscriber, realtimed sends
`resync_required` and removes that channel subscription. A history-reader
failure during the initial subscribe returns `history_read_failed` without
registering the channel; a failure during polling sends that error and closes
the v2 connection with a retryable reason. If its output queue fills before it
can send a control frame, realtimed closes the connection so the client can
reconnect from its saved cursor.
The [SDK consumer](../../sdk/node/README.md#resumable-managed-realtime-preview)
processes messages and acknowledges in order. It persists client-held cursors
locally and lets Gregale persist named subscription cursors. `consumeRealtimeChannels`
can multiplex up to eight independently checkpointed channels over one
socket; a resync reconnects that socket and resumes every channel from its own
cursor. Server-side sockets add an OIDC
bearer header. Browser sockets use
`gregale.realtime.bearer.<signed-JWT>` as a second requested subprotocol because
native WebSockets cannot set that header. Browser credentials are accepted only
for v2 endpoints with a matching non-empty `allowed_origins` policy and a
present `Origin` header. The credential is capped at 3,072 bytes, is verified
through the same endpoint OIDC policy, and is removed before hooks and
subprotocol negotiation. The response selects only `gregale.realtime.v2`.
Configure ingress and proxy access logs to redact `Sec-WebSocket-Protocol` for
this route, since the request header carries the short-lived JWT.

Apid records bounded-cardinality publish outcomes in its standard
operations metrics: `managed_realtime_publish` uses `ok`, `partial`,
`no_subscribers`, `unavailable`, and `canceled`;
`managed_realtime_publish_node` uses `ok`, `endpoint_missing`, `error`, and
`canceled`. Durations are available through
`apid_op_duration_seconds`. These metrics intentionally omit endpoint, channel,
and node identifiers. A node's `ok` outcome means its local queue accepted the
publish; it does not confirm delivery to a client. Partial-success warnings
include fleet counts and are rate-limited to one per minute per apid process.

Inspect the publish outcome and per-node failure rates with:

```promql
sum by (code) (rate(apid_ops_total{op="managed_realtime_publish"}[5m]))
sum by (code) (rate(apid_ops_total{op="managed_realtime_publish_node"}[5m]))
histogram_quantile(0.99, sum by (le) (rate(apid_op_duration_seconds_bucket{op="managed_realtime_publish"}[5m])))
```

Inspect health and counters from the `faas` group:

```
curl --unix-socket /run/faas/realtimed.sock http://localhost/healthz
curl --unix-socket /run/faas/realtimed.sock http://localhost/internal/stats
curl --unix-socket /run/faas/realtimed.sock http://localhost/internal/connections
curl --unix-socket /run/faas/realtimed.sock 'http://localhost/internal/callbacks/dead-letters?limit=100'
```

The dead-letter endpoint returns metadata only. Use the `next_cursor` value as
`after` to list another page. After correcting a callback receiver, POST to
`/internal/callbacks/dead-letters/<event-id>:replay` on this Unix socket to
return one event to the pending outbox. Replay preserves the event ID and
resets its retry budget; HTTP 409 means pending capacity or an active
same-connection delivery must clear first. See the
[callback dead-letter runbook](../runbooks/FaasRealtimeCallbacks.md).

The customer CLI exposes the authenticated connection operations as well:

```sh
# Send raw bytes from a protected pipe; add --binary for binary frames.
printf 'hello' | gregale realtime send my-app ENDPOINT_ID CONNECTION_ID --data-stdin

gregale realtime send-principal my-app ENDPOINT_ID --principal user-123 \
  --data '{"event":"notification"}'

gregale realtime subscribe my-app ENDPOINT_ID CONNECTION_ID room-a
gregale realtime unsubscribe my-app ENDPOINT_ID CONNECTION_ID room-a
gregale realtime close my-app ENDPOINT_ID CONNECTION_ID --reason 'client migrated'

printf '{"event":"refresh"}' | \
  gregale realtime publish my-app ENDPOINT_ID room-a --data-stdin \
    --idempotency-key refresh-event-42

# Retain a sequenced event for v2 reconnect catch-up (preview flags required).
printf '{"event":"job-progress","percent":80}' | \
  gregale realtime publish my-app ENDPOINT_ID room-a --data-stdin \
    --delivery retained --idempotency-key job-42-progress-80

# Inspect active connections before targeting one for management.
gregale realtime connections my-app ENDPOINT_ID --channel room-a --limit 100

# Filter by authenticated principal and continue a large inventory with the
# opaque next_cursor printed by the previous page.
gregale realtime connections my-app ENDPOINT_ID --principal user-123 --limit 100
gregale realtime connections my-app ENDPOINT_ID --principal user-123 --cursor NEXT_CURSOR

# Preview or perform a bounded, auditable drain. A reason is required.
gregale realtime drain my-app ENDPOINT_ID --channel room-a --reason 'deploy migration' --dry-run
gregale realtime drain my-app ENDPOINT_ID --principal user-123 --reason 'account removal'
# If the inventory is partial, explicitly acknowledge that only reachable
# nodes will be acted on.
gregale realtime drain my-app ENDPOINT_ID --reason 'node maintenance' --allow-partial
```

`--data-stdin` is binary-safe and bounded to the same 1 MiB decoded payload
limit enforced by the API. `--data TEXT` is available for small UTF-8
messages; message contents are never included in successful CLI output. Drain
selects by channel, principal, or repeated `--connection-id` flags, defaults
to 100 connections, and caps one request at 1000. A non-dry-run refuses a
partial fleet inventory unless `--allow-partial` is supplied; results report
closed, already-gone, and failed connections separately.

Prometheus scrapes the node-local health listener on `127.0.0.1:9107` in a
single-box deployment. A compute-only deployment binds the same port on the
private node address (restricted to control-plane CIDRs by nftables), or the
value set by `FAAS_REALTIME_HEALTH_LISTEN`. The fixed-cardinality metrics include
`realtimed_current_connections`, accepted/rejected connections, sent/received
messages and bytes, dropped messages, and callback errors. Outbound pressure is
reported by `realtimed_outbound_pending_bytes` and the per-connection
`realtimed_outbound_queue_bytes_limit` gauge. Separate
`realtimed_outbound_queue_count_limit_drops_total` and
`realtimed_outbound_queue_byte_limit_drops_total` counters identify which
bounded queue limit rejected a frame. In a split deployment, the control-plane
Prometheus discovers active realtime owners from apid; sum counters across
nodes and sum the pending-bytes gauge for fleet usage.

```promql
sum(rate(realtimed_outbound_queue_count_limit_drops_total[5m]))
sum(rate(realtimed_outbound_queue_byte_limit_drops_total[5m]))
sum(realtimed_outbound_pending_bytes)
```

Resumable-channel health is visible through `realtimed_current_resume_subscriptions`,
`realtimed_resume_history_reads_total{result}`, and the
`realtimed_resume_history_read_duration_seconds` histogram. History read results
are `success`, `history_unavailable` (the cursor has expired), `error` (the
history reader failed or timed out), and `canceled` (the client or subscription
closed during a read). `realtimed_resume_replayed_messages_total` and
`realtimed_resume_replayed_payload_bytes_total` count retained messages and
decoded payload bytes admitted to node-local output queues; they do not confirm
client receipt. `realtimed_resume_resync_required_total{reason}` separates
`cursor_expired`, `retention_advanced`, and `sequence_gap`. The
`realtimed_resume_slow_consumer_disconnects_total` counter records v2 sockets
closed when a resume frame could not fit in the connection's bounded output
queue. These counters are process-local. Sum counters across realtime nodes and
sum the active-subscription gauge for fleet totals.

```promql
sum(realtimed_current_resume_subscriptions)
sum by (result) (rate(realtimed_resume_history_reads_total[5m]))
histogram_quantile(0.95, sum by (le) (rate(realtimed_resume_history_read_duration_seconds_bucket[5m])))
sum(rate(realtimed_resume_replayed_messages_total[5m]))
sum(rate(realtimed_resume_replayed_payload_bytes_total[5m]))
sum by (reason) (rate(realtimed_resume_resync_required_total[5m]))
sum(rate(realtimed_resume_slow_consumer_disconnects_total[5m]))
```

Set `FAAS_REALTIME_MAX_CONNECTIONS`,
`FAAS_REALTIME_MAX_MESSAGE_BYTES`, `FAAS_REALTIME_OUTBOUND_QUEUE`,
`FAAS_REALTIME_OUTBOUND_QUEUE_BYTES`,
`FAAS_REALTIME_HEARTBEAT`, `FAAS_REALTIME_PONG_WAIT`,
`FAAS_REALTIME_WRITE_WAIT`, `FAAS_REALTIME_MAX_AGE`, and
`FAAS_REALTIME_CALLBACK_TIMEOUT` in the realtimed environment file when
adjusting limits. The outbound byte budget defaults to 4 MiB per connection
and includes a frame currently being written; the message-count limit still
applies independently. `FAAS_REALTIME_CALLBACK_OUTBOX` optionally overrides the
node-local callback spool (default `/var/lib/faas/realtime-callbacks`).
`FAAS_REALTIME_CALLBACK_DEAD_MAX_BYTES` caps retained dead letters
(default 64 MiB). The oldest dead letters are evicted first when the cap is
exceeded, including on startup if an existing spool is over the limit. Copy
records needed for investigation or manual replay before lowering the cap.
Failed durable callbacks retry with jittered exponential backoff from one
second, capped at one minute by default. `FAAS_REALTIME_CALLBACK_RETRY_MAX_INTERVAL`
can raise that cap up to one hour. HTTP 429 and 503 `Retry-After` hints set a
minimum delay, subject to the configured cap; the scheduled time is persisted
with the callback so restarts do not reset the backoff.
The default directory is provisioned as `faas:faas` with mode `0700` and is writable
through the realtimed systemd unit. On the first start after upgrading, realtimed
moves pending events and dead letters from the former `/run/faas/realtime-callbacks`
directory into the persistent spool before accepting connections. A conflicting
event ID stops startup for operator inspection rather than discarding either
copy. Message and disconnect events are fsynced before delivery and replayed
after a daemon restart or host reboot; delivery is at-least-once, and poison
events are retained under the outbox's `dead/` directory after the bounded
retry budget. Unexpected outbox or filesystem errors stop a replay pass, which
realtime retries in process with an exponential delay capped at 30 seconds.
Process shutdown cancels the retry wait. Keep the callback URL
on an ordinary app route so the normal gateway wake path can start a sleeping
application to process an event. Pending callbacks for one connection replay in
WebSocket sequence order, with disconnect after the final message. Existing
`FAAS_REALTIME_CALLBACK_OUTBOX` overrides are unchanged; operators using an
override must provide persistent storage if they need reboot survival. The
spool contains callback payloads and bearer tokens, so keep it out of broadly
readable backups. Callback handlers should deduplicate by event ID because
delivery remains at-least-once. The 64 MiB cap applies to pending callbacks,
and a separate 64 MiB cap applies to retained dead letters. Prometheus exposes
the pending count and bytes, retained dead-letter count and bytes, retention
capacity, eviction count, and last eviction time. It also exposes ready and
delayed replay heads, replay attempts, and successful deliveries. The
`realtimed_callback_replay_supervisor_restarts_total` tracks unexpected replay
loop restarts; `FaasRealtimeCallbackReplayRestarting` warns after repeated
restarts. `FaasRealtimeCallbackReplayStalled` fires when ready replay work
receives no attempts; delayed retries do not trigger it. See the
[callback delivery runbook](../runbooks/FaasRealtimeCallbacks.md). The pending
outbox capacity gauge and
`FaasRealtimeCallbackOutboxNearCapacity` alert warn before pending records hit
the enqueue limit. `realtimed_callback_outbox_full_total` counts events that
could not be persisted; `FaasRealtimeCallbackOutboxFull` pages on any rejection.
`realtimed_callback_outbox_admission_errors_total` counts failures to persist
callbacks caused by local admission or storage errors, and
`FaasRealtimeCallbackOutboxAdmissionFailed` pages on any occurrence.
`realtimed_callback_unpersisted_failures_total` counts failed direct HTTP
callbacks without a durable outbox; `FaasRealtimeCallbackUnpersisted`
pages on any occurrence.
The `FaasRealtimeCallbackDeadLettersPresent`,
`FaasRealtimeCallbackDeadLettersNearCapacity`, and
`FaasRealtimeCallbackDeadLettersEvicted` alerts link to the
[callback dead-letter runbook](../runbooks/FaasRealtimeCallbacks.md).

`/internal/stats` includes callback-pending, callback replay ready/delayed and
attempt/delivery counters, and callback-dead-letter retention counters
alongside the connection and delivery counters.

### Inbox delivery webhooks

Subscribe an app webhook to `realtime.inbox.acknowledged` and
`realtime.inbox.gap` to react to device progress without polling. Apply
`20261008180000001_managed_realtime_inbox_webhooks.sql` before upgrading apid.
This uses the existing app webhook dispatcher; it must be running to deliver
committed events. The inbox preview flags described above still apply.

```sh
gregale webhooks add --app demo --target-url https://backend.example/realtime-events \
  --event realtime.inbox.acknowledged --event realtime.inbox.gap \
  --retry-policy default
gregale webhooks deliveries --app demo WEBHOOK_ID
gregale webhooks retry --app demo WEBHOOK_ID DELIVERY_ID
```

Existing webhook API routes provide the same controls:
`POST /v1/apps/{slug}/webhooks`,
`GET /v1/apps/{slug}/webhooks/{id}/deliveries`, and
`POST /v1/apps/{slug}/webhooks/{id}/deliveries/{delivery_id}/retry`.
An empty event filter includes these events as well. Recipients are enabled,
matching app subscriptions at event creation; later subscription changes do
not replay past device progress.

Both event payloads include `event_id`, `app_id`, `endpoint_id`, `consumer`,
`principal_key`, and `occurred_at`. The principal key is lowercase hexadecimal
SHA-256 of the exact verified principal's UTF-8 bytes. Store that mapping when
publishing if your backend needs to identify the user for a fallback message.
Events contain progress metadata, without notification content or raw identity
claims. The event ID remains stable across retries and across recipient
subscriptions; deduplicate business effects by `event_id`.

An acknowledged event includes `previous_sequence`, `sequence`, and
`message_id` (null if the last message has already been pruned). It is a
cumulative device acknowledgement covering the interval
`(previous_sequence, sequence]`, emitted only when the checkpoint advances.
The event and checkpoint commit atomically. Duplicate ACKs, subscribing with an
initial baseline, and explicit resets do not produce acknowledged events.
It means the SDK's handler finished successfully for that device; it does not
mean a person has read the notification. Devices acknowledge independently.

A gap event includes `sequence`, `oldest_sequence`, and `latest_sequence`.
The API's retention loop checks for devices behind the retained prefix once
per minute in bounded batches, including offline devices whose checkpoints
have not expired. This covers both age expiration and message-count eviction.
One event is recorded per gap episode; further trimming does not repeatedly
notify an unrecovered device. An explicit successful reset, or an ACK that
catches up to the retained floor, allows a later gap to produce another event.
Devices with no checkpoint and checkpoints inactive for 30 days are excluded.
The bounds describe detection time; query the inbox again before recovering.

Delivery uses the existing HMAC-SHA256 signature and backoff policies. The
`default` policy retries after 30 seconds, 2 minutes, 10 minutes, 20 minutes,
1 hour, and 6 hours, subject to the dispatcher's HTTP status classification.
Verify the raw request body before parsing using the
[SDK webhook verifier](../webhook-receiver-verification.md), then commit your
receipt and backend action together. Delivery is at least once and events can
arrive out of order; keep the highest acknowledged sequence per
endpoint/principal/device when updating backend progress. Live send receipts
remain separate from these retained inbox events.

### Notifications with acknowledgement deadlines

Retained principal sends can request a fallback when no device handles the
notification before a deadline. Connected inbox consumers wake immediately;
offline devices can replay later. Configure a signed app webhook first:

```sh
gregale webhooks add --app demo --target-url https://backend.example/notification-fallback \
  --event realtime.inbox.fallback_required --retry-policy default

gregale realtime send-principal demo ENDPOINT_ID --principal USER_ID \
  --delivery retained --message-id order-ready-123 \
  --fallback-after-seconds 60 --data '{"kind":"order_ready","order_id":"123"}'
```

On the principal-send API request, use `delivery: "retained"`, a stable
`message_id`, and `fallback_after_seconds: 60`. Values 1..86400 request a
fallback; zero or omission disables it. Live sends cannot set a deadline.
Apply `20261008190000001_managed_realtime_notification_fallbacks.sql` before
upgrading apid. The existing inbox preview flags and app webhook dispatcher
are required. Clients use `consumeRealtimeInbox` as described above.

Acceptance commits the payload, sequence, and timer together. The response
includes `fallback_deadline`, computed from the original message creation time.
Retries with the same retained ID, bytes, binary flag, and deadline duration
return the original sequence and deadline, without restarting the timer.
Changing the deadline policy for a retained ID returns a conflict, including
adding or removing a deadline. Deduplication lasts while the payload is
retained; after pruning, reuse can create a new sequence and timer. Use unique
IDs for distinct notifications and correlate fallback actions by sequence too.

Any device's successful inbox ACK cancels pending timers in the acknowledged
sequence interval. That cancellation commits with the device checkpoint and
acknowledgement webhook. Connection presence alone, initial cursor baselines,
manual resets, and live-message receipts do not cancel fallback. A connected
but stalled device can therefore trigger fallback. The acknowledgement means
the SDK's application handler completed, rather than that a person read it.

The API worker checks deadlines every five seconds, processing up to 128 per
pass per replica. Deadline processing and ACK cancellation lock the same timer
row: an ACK committed before fallback processing wins. Otherwise processing
commits one `realtime.inbox.fallback_required` event and removes the timer in
one transaction. An event already queued for delivery cannot be recalled by a
later ACK. This is a deadline policy, rather than an exact-time delivery
promise; worker availability, backlog, and webhook retries can delay delivery.

The signed event contains `event_id`, `app_id`, `endpoint_id`, `principal_key`,
`message_id`, `sequence`, `deadline`, and `occurred_at`; `consumer` is empty
because fallback applies to the principal across devices. No notification
payload is copied into the event. Store your business context under the
endpoint, principal key, and sequence, then use the event to enqueue email,
mobile push, or another fallback in your backend. Gregale does not send email
or push itself. Use the existing verifier and deduplicate the backend effect
by `event_id`. Webhooks are at least once and may arrive out of order, including
relative to acknowledgement events.

A fallback send requires an enabled push provider or an app webhook matching
`realtime.inbox.fallback_required` (or an empty event filter). If all eligible
push devices and matching webhook subscriptions are disabled or removed, expired timers remain
pending until a receiver is enabled again or an actual device ACK cancels them.
Recipients are snapshotted when fallback is processed. Pending timers survive
payload eviction and are capped at 256 per principal; they are removed after
cancellation or event creation, and cascade away with endpoint deletion.
Use `gregale webhooks deliveries --app demo WEBHOOK_ID` and
`gregale webhooks retry --app demo WEBHOOK_ID DELIVERY_ID` to inspect and retry
fallback delivery.

### Named temporary client signals

The existing v2 `signal` frame now supports `name` and `ttl_ms`:

```json
{"type":"signal","channel":"room-a","name":"typing","ttl_ms":5000,"data":true}
```

The authenticated sender must already have an authorized channel subscription.
The realtime node derives its member ID and stamps `updated_at` and
`expires_at`; clients cannot choose an identity or an expiry timestamp. Names
are 1..64 ASCII letters, digits, `_`, `-`, `.`, or `:`. TTL is an integer
0..30000 milliseconds. A named frame with zero TTL clears that member's named
value; ordinary unnamed signals retain their existing behavior. Payloads are
JSON up to 2 KiB. The existing 20-per-second connection and 50-per-second
channel limits per sending node apply to updates and clears alike.

Signals are transient channel activity. They share fleet relay and current v2
subscription authorization, and have no history, inbox storage, ACK, or
reconnect snapshot. Receivers treat a new value as replacing the same
channel/member/name. `expires_at` is carried unchanged across the fleet;
receiving nodes discard already-expired positive-lifetime relays. Explicit
clears still propagate. No expiry event is retained or replayed.

Use the Node/browser SDK's `sendTemporarySignal`, `clearTemporarySignal`, and
`createRealtimeSignalTracker` for automatic local removal when no further
frames arrive. Custom clients must implement that expiry behavior themselves.
See the [SDK example](../../sdk/node/README.md#typing-indicators-and-temporary-client-signals)
for timers, presence-leave handling, reconnect cleanup, and throttling. Signal
state is best effort; applications should refresh active typing or cursor
indicators before expiry. Upgrade apid and all realtime nodes before publishing
named signals and retain the existing resume preview configuration. No database
migration is needed.

### Editing and deleting retained messages

Retained channels and principal inboxes support versioned edits and deletions.
Use the caller's stable channel idempotency key or inbox message ID; channel
messages published without a key cannot be edited. Initial messages have
version 1. Apply `20261008200000001_managed_realtime_message_mutations.sql`
before upgrading apid, realtime nodes, and the SDK/application handlers.
The existing retained and resume preview flags apply.

```sh
gregale realtime edit-message demo ENDPOINT_ID --channel room-a \
  --message-id chat-123 --expected-version 1 --data 'Corrected text'
gregale realtime delete-message demo ENDPOINT_ID --channel room-a \
  --message-id chat-123 --expected-version 2
# Use --principal USER_ID instead of --channel for a principal inbox.
```

API routes accept PATCH for replacement and DELETE for redaction:

- `/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/retained-messages/{message_id}`
- `/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/{message_id}?principal=USER_ID`

Both require `expected_version`. PATCH also accepts `data_base64` and `binary`,
replacing the complete payload; DELETE accepts no payload. Replacement bytes
must fit both the endpoint limit and the retained 4 KiB limit. Authorization
uses the existing account-scoped deployment-write surface and MFA. The
endpoint must be enabled. A stale version, restoring a deleted message, or
republishing a mutated ID returns 409. A missing or expired target returns
404. The mutation response includes `message_id`, `version`, `sequence`,
`event`, and `deleted`. Each successful edit/delete increments the version;
a delete of an already-deleted target at its current version returns that
existing tombstone without allocating another sequence.

Each mutation commits the latest snapshot and a new ordered stream entry
atomically under the stream head lock. Connected v2 clients receive it through
their existing history pumps (normally within the five-second polling
interval). A client that acknowledged the original still receives the new
sequence; prior acknowledgements keep their original meaning. SDK `onMessage`
receives `event: 'created' | 'updated' | 'deleted'`, `version`, `deleted`, and
`targetMessageId`, as well as the stream sequence and bytes. Channel
`messageId` remains the delivery ID for that sequence; `targetMessageId` is the
stable publisher key used to update the application's record. Inbox
`messageId` and `targetMessageId` both identify the notification.

History reads expose matching version/event/deletion metadata. Earlier
retained copies of a target contain the latest payload and version; all their
payloads are emptied on deletion, with a tombstone kept for ordered replay.
Multiple stream entries can therefore project the same latest version. Store
application state by endpoint/stream/target ID and apply a version only when
it exceeds the recorded version. Continue processing stream sequences and
ACKing duplicate-version projections. Deletion is terminal while the target
remains retained; publish a new stable ID to create another message.
Mutations consume the same stream limits and retention as ordinary entries.

An edit leaves the original inbox fallback deadline unchanged. A deletion
cancels that target's pending timers in the same transaction. Already-queued
fallback webhooks and websocket payloads cannot be recalled. Redaction removes
payloads from retained storage, rather than from copies an application already
received. Live-only messages are outside this feature. Custom clients and
application handlers must recognize tombstones before enabling mutations.
The migration is forward-only so rollback cannot discard redaction metadata.

### Read receipts and unread counts

Read progress records a person's explicit "seen through sequence N" signal.
It is shared by all devices for the verified principal, independently of
connection ACKs, device inbox checkpoints, and notification fallback timers.
Apply `20261008210000001_managed_realtime_read_progress.sql`, then upgrade apid,
realtime nodes, and the SDK. The retained/resume preview flags remain required.

```sh
gregale realtime read-progress demo ENDPOINT_ID --principal USER_ID --channel room-a
gregale realtime mark-read demo ENDPOINT_ID --principal USER_ID --channel room-a --sequence 42
# Omit --channel to inspect or mark the principal's notification inbox.
gregale webhooks add --app demo --target-url https://backend.example/read-events \
  --event realtime.message.read --retry-policy default
```

GET and POST use these account-scoped API routes:

- `/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/read-progress?principal=USER_ID`
- `/v1/apps/{slug}/realtime/endpoints/{id}/principals/inbox/read-progress?principal=USER_ID`

POST accepts `{"sequence":42}` and uses deployment-write authorization and
MFA. GET uses the read scope. Both return `sequence`, `unread`,
`oldest_sequence`, `latest_sequence`, `history_unavailable`, and optional
`updated_at`. A missing marker returns sequence zero without creating storage.
Writes are monotonic: repeats and older sequences return current progress,
without another durable read event. Future sequences are rejected. Markers
persist for the endpoint's lifetime, with a cap of 1024 principal/stream
markers per endpoint; deleting the endpoint removes them.

Unread counts cover distinct, undeleted logical messages in the retained
prefix after the read watermark. Repeated projections of a stable message ID
count once; messages without IDs count independently. A new edit sequence can
make that logical message unread again, while deleted messages contribute
zero. The count includes all messages regardless of who authored them.
If retention has removed older unseen messages, `history_unavailable` is true
and the count covers only remaining retained messages. Read progress never
silently skips that gap. Applications choose whether opening a view should
advance to a retained sequence after rebuilding missing state.

For authenticated v2 clients, `read` and `read_state` target a subscribed
channel, while `inbox_read` and `inbox_read_state` target the authenticated
principal inbox. A read write includes `sequence`. The node derives principal
identity, verifies the subscription, and rejects a write above the highest
sequence sent on that subscription. A connection can make up to 20 read
requests per second. API writes are backend assertions; websocket writes are
user signals, so applications should send them only after an actual view or
interaction. Neither proves that a person understood the content.

Subscriptions can opt into live events with `read_receipts:true`. The SDK
sets this when read callbacks are supplied, and making a read request also
opts in. Older clients receive no unsolicited read frames. `read_receipt`
contains the watermark and counts, with the hashed principal in `member_id`.
Channel receipts go to other opted-in authorized subscribers, including the
sender. Inbox receipts go only to opted-in devices for that same principal;
the fleet relay filters them on each node and never puts inbox receipts into
public channel subscriptions. No raw identity claims are broadcast. The SDK
maps `member_id` to `readerId` and ignores older watermarks/snapshots. Your
application supplies the mapping from principal keys to display names.

Live read events are best effort and have no replay. The SDK requests the
user's current state after reconnect when `onReadProgress` is provided;
`refreshReadProgress()` can query it again as new messages arrive. Channel
callbacks may also describe other readers: use `readerId` to keep per-user
"seen" indicators and avoid treating every channel receipt as your own badge.
Use `onReadError` to handle rate limits, storage failures, or relay failures.
A relay failure can follow a successful persisted write, so refresh before
retrying. Reissuing a mark is safe because it cannot move progress backward.

A genuine watermark advance commits a signed `realtime.message.read` app
webhook in the same transaction. Its payload includes `event_id`, `app_id`,
`endpoint_id`, `principal_key`, `channel` (empty for inbox), `inbox`,
`previous_sequence`, `sequence`, `unread`, and `occurred_at`; `consumer` is
empty because read progress is shared across devices. The existing webhook
verification, retries, history, and manual replay controls apply. Deduplicate
by event ID and keep the highest sequence per reader/stream because deliveries
can arrive out of order. Reading does not cancel fallback timers or advance
message-delivery ACKs. See the [SDK example](../../sdk/node/README.md#read-receipts).

### Push notification fallback (FCM, APNs, Web Push)

Managed realtime can send a push notification when a retained principal inbox
message reaches its acknowledgement deadline. Configure one or more push providers
on the endpoint, then let clients register their device token over the authenticated
v2 inbox socket. Your backend still publishes the message through `send-principal`
with `--delivery retained`, a stable message ID, and `--fallback-after-seconds`.

Configure a provider using a JSON file or stdin; avoid passing credentials in shell
arguments. Provider credentials and device tokens are sealed with the host age key.
Read APIs, successful command output, audit records, and delivery history exclude
credentials and tokens.

```sh
gregale realtime push configure demo ENDPOINT_ID --provider fcm --file fcm.json
gregale realtime push providers demo ENDPOINT_ID
gregale realtime push devices demo ENDPOINT_ID --principal user-42
gregale realtime push deliveries demo ENDPOINT_ID --principal user-42
```

Provider file shapes (the CLI wraps these in `config` and `enabled`):

```json
{"provider":"fcm","project_id":"my-project","service_account_json":{"type":"service_account","client_email":"...","private_key":"...","token_uri":"https://oauth2.googleapis.com/token"},"title":"New notification","body":"Open the app to view it."}
```

```json
{"provider":"apns","team_id":"ABCDEFGHIJ","key_id":"KLMNOPQRST","topic":"com.example.app","private_key":"-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----","sandbox":false}
```

```json
{"provider":"webpush","subject":"mailto:push@example.com","private_key":"BASE64URL_P256_PRIVATE_SCALAR"}
```

FCM uses HTTP v1 and a service account authorized to send messages for the project.
APNs uses a P-256 `.p8` signing key and the app's bundle ID as its topic. Web Push
uses a VAPID P-256 key pair; the browser subscribes using the matching public key.
Use unpadded base64url for the Web Push private scalar, `p256dh`, and `auth` fields.
Web Push payloads use RFC 8291 encryption and RFC 8188 `aes128gcm` framing.

Browser/Node SDK consumers receive push actions after subscribing:

```ts
import { realtimeWebPushRegistration } from '@gregale/sdk-node/browser';

const inbox = {
  consumerId: 'browser-main',
  onMessage: async message => { await saveInboxMessage(message); },
  onPushActions: actions => {
    // Get user permission and create the PushSubscription in your app first.
    actions.registerPush(realtimeWebPushRegistration(subscription.toJSON()));
    // Mobile integrations instead register { provider: 'fcm' | 'apns', target: { token } }.
  },
  onPushRegistered: registered => { /* registration/unregistration confirmed */ },
  onPushError: code => { /* display or record a registration failure */ },
};
```

`unregisterPush()` removes the current consumer's registration. Socket registration frames are limited to 4096 bytes. Device names match
inbox consumer names; a socket can register only its verified principal and active
consumer. Repeat registration with the same provider and token is idempotent and
preserves queued deliveries. A changed token rotates its version and cancels work
for the old token. The application owns notification permission, service worker
installation, notification display/click handling, and token refresh.

Account APIs are under `/v1/apps/{slug}/realtime/endpoints/{id}/push`:

| Operation | Route | Body |
|---|---|---|
| List providers | `GET /providers` | none |
| Configure or disable provider | `PUT /providers/{provider}` | `{"config":{...},"enabled":true}` |
| List devices | `GET /devices?principal=...` | none |
| Register device | `PUT /devices/{device}?principal=...` | `{"provider":"fcm","target":{"token":"..."}}` |
| Unregister device | `DELETE /devices/{device}?principal=...` | none |
| Recent delivery history | `GET /deliveries?principal=...` | none |

CLI backend registration accepts the same registration JSON via
`gregale realtime push register APP ENDPOINT --principal ID --device NAME --file FILE`.
Use `unregister` with the principal and device name to remove it. Configure with
`--enabled=false` to disable a provider and cancel its queued work. Re-enabling
allows future deadlines to enqueue; it does not replay cancelled deliveries.

At deadline processing, current enabled devices and providers are snapshotted.
Matching fallback webhooks still fire alongside built-in push; avoid sending a
second push from that webhook unless duplicate delivery is intentional. With no
eligible device or webhook, the deadline remains pending. ACKs cancel both timers
and pending/in-flight ledger work through the acknowledged sequence interval;
read receipts do not cancel push. Message deletion also cancels queued work.
Already-issued HTTP requests and accepted pushes cannot be recalled.

Push notifications contain a configured title/body, endpoint ID, logical message
ID, sequence, and stable delivery ID. They never copy the inbox message body. Apps
fetch the inbox on open and deduplicate by delivery ID. Delivery is at least once:
a worker crash after provider acceptance can cause a retry. `sent` means accepted
by the provider, not displayed, read, or acknowledged by the user.

Each endpoint supports 16 devices per principal and 1024 devices total. The ledger
holds at most 4096 deliveries per endpoint. Completed history lasts up to seven
days and is evicted oldest first when space is needed. Pending work expires after
24 hours; when all capacity is occupied by unfinished jobs, fallback deadlines
remain pending rather than dropping notifications. The history API returns the
latest 100 deliveries for the supplied principal.

The worker claims eight jobs per pass with 60-second leases and bounded request
timeouts. Transient failures retry after 30 seconds, 2 minutes, 10 minutes,
20 minutes, 1 hour, and 6 hours, for at most seven claimed attempts (including recovered worker leases). Provider
401/403 responses also retry so operators can repair credentials. Invalid-token
responses disable the matching token version and erase its sealed token;
a newer registration is protected from stale responses. Web Push destinations
must be public HTTPS endpoints; redirects are rejected and addresses are checked
again at dial time. Provider response bodies are never copied into history/logs.

Apply migration `20261008220000001_managed_realtime_push.sql` before starting an
upgraded apid. This feature shares the retained-history preview gate. No provider
requests are made until configured credentials, a registered device, and a due
fallback are present.

### Notification preferences and quiet hours

A verified user can control push notifications across devices on one endpoint.
New users default to enabled notifications, every category, every registered
device, and no quiet hours. Inbox messages remain available regardless of push
preferences.

```json
{
  "enabled": true,
  "categories": {"chat": true, "jobs": false, "notifications": true},
  "devices": ["browser-main", "phone-main"],
  "quiet_hours": {"timezone": "Europe/Rome", "start": "22:00", "end": "07:00"}
}
```

The document is replaced as a whole. `enabled` is required. Unlisted categories
are enabled; category names use 1..64 lowercase ASCII letters, digits, dots,
underscores, or hyphens. `devices: null` (or omitted) selects all registered
consumer names; `devices: []` selects none. A selected name can be registered
later. `quiet_hours: null` (or omitted) disables quiet hours. Daily intervals
include the start and exclude the end; overnight windows are supported, and
start/end must differ. Use a named timezone, such as `Europe/Rome` or `UTC`.
Daylight-saving gaps and repeated hours are evaluated as actual instants in that
zone. `Local` is rejected because it depends on the server's configuration.

Label retained notifications when requesting push fallback:

```sh
gregale realtime send-principal demo ENDPOINT_ID --principal user-42 \
  --delivery retained --message-id job-42 --data 'Job finished' \
  --fallback-after-seconds 60 --notification-category jobs

gregale realtime push set-preferences demo ENDPOINT_ID --principal user-42 \
  --file preferences.json
gregale realtime push preferences demo ENDPOINT_ID --principal user-42
```

The REST send body uses `notification_category` alongside
`fallback_after_seconds`. Omitted categories default to `notifications`.
Category is part of the retained message's deduplication policy: retrying the
same message ID with a different category returns a conflict. Push payloads and
delivery history expose the category.

Preferences are available through `GET` and `PUT`
`/v1/apps/{slug}/realtime/endpoints/{id}/push/preferences?principal=...` using
account authorization. The PUT body is the preference document above. A v2
inbox client can read and replace only its verified user's preferences through
`inbox_preferences_get` and `inbox_preferences_put` (the latter sends the
preference document in `data`, both include the active `consumer` name).

```ts
inbox: {
  consumerId: 'phone-main',
  onMessage: async message => { await saveMessage(message); },
  onPushActions: actions => {
    actions.setNotificationPreferences({
      enabled: true,
      categories: { chat: true, jobs: false },
      devices: null,
      quiet_hours: { timezone: 'Europe/Rome', start: '22:00', end: '07:00' },
    });
    // actions.refreshNotificationPreferences() reads the latest document.
  },
  onNotificationPreferences: preferences => { /* refresh the settings screen */ },
  onNotificationPreferencesError: code => { /* handle a rejected update */ },
}
```

Supplying `onNotificationPreferences` automatically fetches preferences after
subscription/reconnect. Updates are confirmed through that callback. The SDK
and socket enforce the existing 4096-byte client frame limit; the REST PUT also
limits preference bodies to 4096 bytes. Each document allows 32 category
overrides and 16 selected devices; each endpoint allows 256 persisted user
preference documents. Resetting settings replaces the existing document with
`{"enabled":true}` and does not allocate another slot.

The dispatcher checks current preferences before attempting provider delivery.
Muted categories, disabled notifications, and unselected devices produce
`cancelled` history with `preferences_muted`. Quiet hours keep delivery pending,
set `next_attempt` to the next allowed minute and record `quiet_hours` without
consuming a provider retry attempt. Setting changes wake quiet-hour jobs so an
earlier end time or a new mute takes effect on the next worker pass. Completed
or muted deliveries are not replayed when settings are re-enabled.

Deferred notifications retain their delivery IDs and remain subject to ACK,
message deletion, device rotation, and provider-disable cancellation. The normal
24-hour delivery expiry can extend to one hour after quiet hours end, bounded
by 72 hours from job creation. Deferred alerts do not extend the inbox's message
retention: an app should rebuild from its backend if opening a notification
requires inbox resynchronization. Requests already sent to a provider cannot be
recalled. Fallback webhooks remain backend events; handlers that send their own
notifications should consult these preferences too.

Apply `20261008230000001_managed_realtime_push_preferences.sql` after the push
migration and upgrade push workers before exposing preference controls. The
migration backfills existing notifications into the `notifications` category
and keeps their original 24-hour expiry.

### Notification grouping and digests

Push fallback can combine related notifications into summaries while retaining
individual inbox events. A backend can attach an opaque conversation, job or
project key and an optional display label to each retained notification:

```sh
gregale realtime send-principal demo ENDPOINT_ID --principal user-42 \
  --delivery retained --message-id chat-42 --data 'New chat event' \
  --fallback-after-seconds 60 --notification-category chat \
  --notification-group project-alpha --notification-group-label 'Project Alpha'
```

The REST send fields are `notification_group_key` and
`notification_group_label`. Both are bounded to 128 UTF-8 bytes with no NUL,
CR/LF, or leading/trailing whitespace. A label requires a key, and grouping
metadata requires a nonzero fallback deadline. Group metadata joins category,
payload, and fallback policy in retained-send idempotency: changing it while
retrying the same logical message ID returns a conflict.

Set delivery preferences through the existing preferences API, CLI, or SDK:

```json
{
  "enabled": true,
  "categories": {"chat": true, "jobs": true},
  "devices": null,
  "quiet_hours": {"timezone": "Europe/Rome", "start": "22:00", "end": "07:00"},
  "digest_interval_seconds": 300,
  "summarize_quiet_hours": true
}
```

`digest_interval_seconds` accepts 0 (immediate, the default), 300 (five minutes),
or 3600 (hourly). Digests use fixed UTC windows based on when fallback jobs
enter the push ledger: a job enqueued at 12:02 belongs to the window ending
12:05 for five-minute delivery. The next permitted time accounts for quiet
hours too. Changing preferences wakes deferred jobs to reconsider their schedule. Interval
changes apply to jobs whose batch has not frozen; frozen batches retain their
identity and original window through retries.

`summarize_quiet_hours` defaults to true. Messages released from the same quiet
window can be combined even with immediate delivery. Set it to false to send
individual alerts after quiet hours; a nonzero digest interval still batches
alerts according to that interval.

A digest combines jobs for the same endpoint, verified user, device/token
version, provider, category, group key and delivery window. An omitted group
key groups by category. Different conversations/projects remain separate.
The latest remaining message supplies the display label and inbox sequence.
For multiple chat events, the provider notification body becomes, for example,
`12 new messages in Project Alpha`; jobs use `12 job updates`, and other
categories use `12 new notifications`. A one-message digest keeps the configured
provider body. The configured title applies to every digest.

Each batch contains at most 128 ledger rows. Larger windows produce multiple
summaries. Membership freezes when the batch is acquired; later arrivals form
a subsequent batch. Each batch has a durable `digest_id` shared by its member
rows, and history includes `digest_count`, `group_key`, and `group_label`.
The count is the number of distinct logical messages in the most recently
prepared attempt, not a count to add across rows that share a digest ID.

ACKs and message deletion remove individual members before the payload is
prepared. Acknowledging one member does not cancel other members, including
when the original claim's representative is removed. If every member has been
acknowledged, no provider request is issued. Preferences, quiet hours and device
registration are checked again before sending; muted work remains cancelled.
Scheduling delays consume no provider attempt, while provider failures retain
the existing per-message retry limit. Completion updates all remaining leased
members in one transaction.

Push payloads contain `delivery_id` equal to the digest ID, `message_count`,
`category`, `group_key`, and the latest unacknowledged message ID/sequence.
FCM data encodes the count and sequence as strings; APNs and Web Push encode
numbers. Individual message bodies are excluded. Apps should fetch the inbox
on open and deduplicate provider retries by `delivery_id`. An ACK cannot recall
an already-issued request. A crash after provider acceptance can cause the same
digest to be sent again with a smaller count if some members were acknowledged
in the meantime.

APNs uses `thread-id` for category/group organization. FCM Android uses the
stable digest ID as its notification tag to replace duplicates of that digest.
Web Push includes `tag` equal to the digest ID; the service worker should pass
it to `showNotification()` so retries replace the same alert. Different digests
retain separate IDs and are not silently overwritten.

The existing 4096-row endpoint ledger bound, expiry limits, and history retention
still apply. Scheduling a digest does not extend inbox retention or affect its
ACK cursor. Apps must handle inbox resynchronization if a delayed alert opens
beyond retained history.

Apply `20261009000000001_managed_realtime_push_digests.sql` after the preferences
migration and upgrade push workers before exposing digest controls. Existing
notifications receive an empty group key; new preference fields remain optional.
Use the upgraded SDK when editing digest preferences, since preference updates
replace the full document.

### Notification priority and urgent opt-in

Retained sends with a fallback deadline accept `notification_priority`:
`low`, `normal` (when omitted), or `urgent`. The CLI exposes the same choice:

```sh
gregale realtime send-principal APP_SLUG ENDPOINT_ID --principal USER_ID \
  --delivery retained --message-id incident-123 --data '{"incident":"service_down"}' \
  --fallback-after-seconds 1 --notification-category incidents \
  --notification-priority urgent
```

Eligible push jobs are claimed in urgent, normal, then low order. Priority does
not reorder inbox sequences or shorten the fallback acknowledgement deadline.
Normal and low alerts keep the user's quiet-hour and digest scheduling. Urgent
alerts do too unless the user explicitly enables `allow_urgent_bypass: true` in
their complete notification preference document. The default is false:

```json
{
  "enabled": true,
  "allow_urgent_bypass": true,
  "digest_interval_seconds": 300,
  "quiet_hours": {"timezone": "Europe/Rome", "start": "22:00", "end": "07:00"}
}
```

With this opt-in, urgent alerts become eligible after their fallback deadline
without waiting for quiet hours or a digest boundary. Master, category and device
mutes still apply. Inbox acknowledgements still cancel individual queued alerts.
Preferences are checked again before provider delivery; changing the opt-in wakes
alerts deferred by quiet hours or digests for reconsideration. Revoking it restores
current quiet-hour and digest delays for queued urgent work. Alerts already handed
to a provider cannot be recalled.

Digest groups never mix priorities. Delivery history and FCM, APNs and WebPush
payloads expose `priority`. This setting controls Gregale scheduling; it does not
request operating-system critical-alert or Do Not Disturb privileges. Reusing a
message ID with a different priority is an idempotency conflict.

Apply `20261009010000001_managed_realtime_notification_priority.sql` after the
digest migration before running this server version. Existing rows become normal
priority. The migration has not been applied as part of this change.

### Expiring push notifications

Retained fallback sends accept `notification_ttl_seconds`, exposed by the CLI as
`--notification-ttl-seconds`. A positive lifetime must exceed
`fallback_after_seconds` and be at most 259200 seconds (three days). It starts at
message publication, including time waiting for acknowledgement and delivery.
Omit it or use zero to keep the existing default expiration policy.

```sh
gregale realtime send-principal APP_SLUG ENDPOINT_ID --principal USER_ID \
  --delivery retained --message-id job-123 --data '{"progress":50}' \
  --fallback-after-seconds 30 --notification-category jobs \
  --notification-ttl-seconds 300
```

Explicit lifetimes are hard deadlines: quiet hours and digest delays cannot
extend them. Expired queued alerts are cancelled with delivery-history reason
`expired`; digest preparation removes expired members while keeping the remaining
alerts. Retries are scheduled only if their next attempt precedes expiration.
An alert whose scheduled delivery cannot occur before its deadline is cancelled
with reason `expired` immediately. Expiration does not delete inbox messages or
change their retention, sequence, or acknowledgement behavior. Reusing a message
ID with a different lifetime is an idempotency conflict. Provider requests already
in flight cannot be recalled.

The migration `20261009020000001_managed_realtime_notification_ttl.sql` follows
the notification-priority migration. It has not been applied in this workspace.

### Notification collapse keys

For changing state such as job progress or live scores, retained fallback sends
can set `notification_collapse_key`. The CLI exposes
`--notification-collapse-key`; keys accept up to 128 UTF-8 bytes, without leading
or trailing whitespace, NUL, CR or LF. Omit the key to keep separate alerts.

```sh
gregale realtime send-principal APP_SLUG ENDPOINT_ID --principal USER_ID \
  --delivery retained --message-id job-123-update-2 --data '{"progress":75}' \
  --fallback-after-seconds 30 --notification-category jobs \
  --notification-collapse-key job-123 --notification-ttl-seconds 300
```

When a live fallback becomes a per-device push job, it supersedes older queued
jobs with the same endpoint, recipient, device, category, priority and collapse
key. Inbox sequence defines freshness, so late-draining older timers cannot
replace newer queued updates. Use distinct message IDs for successive updates;
changing a collapse key while reusing a message ID is an idempotency conflict.
Normal updates cannot supersede urgent alerts, and already-expired incoming
updates do not replace queued alerts. Keys are independent of digest group keys.

Superseded deliveries have status `cancelled` and reason `superseded` in delivery
history, which also exposes `collapse_key`. Cancelling a leased delivery fences
its worker before provider submission. Digest preparation drops superseded members
and sends remaining members normally. All inbox messages retain their sequences,
retention and acknowledgement behavior. Quiet hours, mute settings, priority and
expiration apply to the replacement alert as usual. Alerts already submitted to
a provider cannot be recalled; collapse affects Gregale's queued push deliveries.
Fallback webhook consumers handle any collapse of their own downstream alerts.

Apply `20261009030000001_managed_realtime_notification_collapse.sql` after the
notification-TTL migration before running this version. Existing alerts have no
collapse key. The migration has not been applied in this workspace.

### Per-user push rate limits

Notification preferences accept an optional `rate_limit`. Omit it or set it to
null to disable throttling. Configure `max_notifications` from 1 to 100 and a
`window_seconds` of 60, 300 or 3600. For example:

```json
{
  "enabled": true,
  "rate_limit": {
    "max_notifications": 5,
    "window_seconds": 60,
    "allow_urgent_bypass": false
  }
}
```

The quota is shared by a principal's devices within one endpoint. Each digest
submitted to one device consumes one slot, regardless of its member count.
Windows align to UTC minute, five-minute or hourly boundaries; this is a fixed
window limit, so a burst can straddle a boundary. Due alerts with compatible
category, priority, group key and creation window can combine into a digest, up
to the existing 128-member limit. Frozen digests keep their identities.

When the quota is full, delivery history shows `rate_limited` and `next_attempt`
at the next window boundary. Waiting alerts remain subject to acknowledgements,
collapse keys, mute settings, quiet hours and expiration. If the next eligible
window is at or beyond an alert's expiration, it is cancelled with reason
`expired`. Updating preferences wakes throttled alerts for reconsideration.

Rate-limit `allow_urgent_bypass` defaults to false and is independent of the
existing top-level urgent quiet-hour/digest opt-in. Set it to true to exempt
urgent alerts from this quota; category and device mutes still apply.

A slot is reserved before provider submission so concurrent workers cannot exceed
the configured quota. Preparation rechecks and retries of the same digest in the
same window reuse its reservation. A retry in a later window needs a new slot.
Reservations remain consumed if provider submission fails or the worker stops;
this conservatively limits logical delivery attempts rather than counting only
successful notifications. Reservations are persisted and cleaned after two hours.
No provider request already in flight can be recalled.

Apply `20261009040000001_managed_realtime_push_rate_limits.sql` after the collapse
migration. It has not been applied in this workspace.

### Scheduled notifications

Retained fallback sends accept `notification_not_before`, an RFC3339 timestamp
with a timezone. The CLI exposes `--notification-not-before`. Schedule up to 48
hours ahead; past timestamps are accepted and behave as immediately eligible
after the normal acknowledgement deadline. Omit it to keep existing behavior.

```sh
gregale realtime send-principal APP_SLUG ENDPOINT_ID --principal USER_ID \
  --delivery retained --message-id appointment-123 --data '{"reminder":"appointment"}' \
  --fallback-after-seconds 30 --notification-not-before '2026-10-09T09:00:00Z'
```

Inbox publication happens immediately. Built-in push waits until both the
acknowledgement deadline and `notification_not_before` have passed. After the
fallback timer enqueues a per-device job, delivery history exposes `not_before`,
reason `scheduled` and `next_attempt`. Quiet hours, digest windows and rate limits
may delay delivery further. Digest scheduling uses the scheduled time when it is
later than job creation. Urgent bypass never bypasses the explicit schedule.

ACKs cancel scheduled jobs as usual. A newer alert with a matching collapse key
can supersede scheduled work. An explicit `notification_ttl_seconds` still starts
at publication and must extend beyond the scheduled instant; delays beyond the
TTL cancel the alert with reason `expired`. Without an explicit TTL, scheduled
jobs retain at least one hour of delivery eligibility after the scheduled time,
subject to existing preference-delay bounds. Scheduling does not extend inbox
retention, so select a schedule compatible with your application's inbox needs.
Reusing a message ID with a different scheduled instant is an idempotency conflict;
equivalent timezone offsets are normalized to the same UTC instant.

Scheduling controls Gregale's built-in push. Fallback webhooks still fire at their
acknowledgement deadline and include `notification_not_before`; external fallback
consumers must honor that value when scheduling their own deliveries. Provider
requests already in flight cannot be recalled.

Apply `20261009050000001_managed_realtime_scheduled_notifications.sql` after the
rate-limit migration. It has not been applied in this workspace.

### Cancel or reschedule queued notifications

Publishers can control existing work without deleting the retained inbox message:

- `DELETE /v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}?principal=USER_ID`
  cancels pending fallback timers and active device deliveries.
- `PUT` to the same path accepts
  `{"notification_not_before":"2026-10-09T09:00:00Z"}` to reschedule active work.

These preview endpoints use the existing account authentication, MFA policy and
`ScopesDeployWriteSurface` authorization. They return
`{"fallbacks":1,"deliveries":2}` with counts of affected timers and device jobs.
No active work returns zero counts; cancellation is repeatable. Terminal jobs
are never revived. Inbox payloads, sequences, acknowledgements and publication
idempotency metadata remain unchanged. Publishing the original message ID again
does not recreate a cancelled timer.

Rescheduling accepts RFC3339 timestamps up to 48 hours ahead. It must precede any
explicit expiration and stay within the existing 72-hour job scheduling bound.
Expired jobs are not rescheduled. Invalid expiration changes fail atomically across
the recipient's active devices and timer. A rescheduled alert waits for the
original acknowledgement deadline when its fallback timer has not fired yet.
The new time is an earliest delivery time: current preferences and quotas can
still defer it. Past timestamps request delivery as soon as those rules allow.

Active device deliveries are detached from their old digest and lease, then
reconsidered under the new schedule. Other digest members continue normally.
Cancellation records `cancelled_by_publisher`; rescheduling updates `not_before`,
`next_attempt` and the `scheduled` or `rescheduled` reason. Both actions fence old
worker leases, but a provider request already in flight cannot be recalled.
Already-emitted fallback webhook events are not retracted; external delivery
systems must implement their own cancellation or rescheduling.

The Go API client provides `CancelManagedRealtimeNotification` and
`RescheduleManagedRealtimeNotification`. No additional migration is required
beyond the scheduled-notification migration.

### Notification timelines

Fetch state changes across a recipient's devices with:

`GET /v1/apps/{slug}/realtime/endpoints/{id}/push/notifications/{message_id}/timeline?principal=USER_ID`

The preview endpoint requires existing account authentication, MFA policy and
`ScopesReadSurface` authorization. It returns up to 100 events, newest first.
For older entries, pass `before` equal to the last event's `id`; the cursor is
exclusive. Unknown messages or messages with no retained events return an empty
array. The Go client exposes `ListManagedRealtimeNotificationTimeline`.

Each event includes its ID, message ID, timestamp, delivery status or timer event,
reason, attempt count, provider HTTP status, earliest scheduled time and next
attempt time. Device and delivery IDs identify per-device transitions. Reasons
include `scheduled`, `rate_limited`, `cancelled_by_publisher`, `superseded`,
`expired` and provider failures. Pending/sending/sent/failed/cancelled transitions
show queue attempts and outcomes; sending is a queue lease, not proof that a
provider received a request. Timer events are `fallback_scheduled`,
`fallback_rescheduled` and `fallback_removed`. Removal can mean ACK, cancellation,
cleanup or handoff into device deliveries; correlate it with delivery events
rather than interpreting removal alone as successful delivery.

For example, a reminder can show scheduled, sending, rate-limited, rescheduled
and sent events. A digest produces transitions for its individual delivery rows.
Lease and digest bookkeeping alone is omitted when exposed state has not changed.
No inbox payloads, push tokens or provider credentials are included.

PostgreSQL triggers record events in the state-change transaction, including
changes made by fallback draining, ACKs and notification control. Memory storage
records through shared mutation helpers. History retains at most seven days and
8192 recent events per endpoint, so busy endpoints may lose older events sooner.
This is bounded diagnostic history, not a permanent audit log. Existing records
are not backfilled; events start when the feature is enabled. Endpoint deletion
removes its timeline.

Apply `20261009060000001_managed_realtime_notification_timeline.sql` after the
scheduled-notification migration. It has not been applied in this workspace.

### Notification delivery-status webhooks

App webhook subscriptions now accept:

| Event | Meaning |
| --- | --- |
| `realtime.notification.sent` | Push provider accepted the request. |
| `realtime.notification.failed` | Delivery permanently failed or cannot retry. |
| `realtime.notification.expired` | Expiration or preference-delay bound ended eligibility. |
| `realtime.notification.cancelled` | ACK, publisher cancellation, mute, device removal or other cancellation. |
| `realtime.notification.superseded` | A newer update replaced the queued delivery. |

Use the existing app webhook configuration and event filters. An empty filter
also receives these events. Outcomes enter the existing transactional outbox,
with its recipient snapshot, signing, retry and delivery-ledger behavior. Each
outcome has a stable `event_id` across webhook delivery retries. Deduplicate by
that ID; consumers should tolerate events arriving out of order.

Payloads contain `event_id`, `app_id`, `endpoint_id`, hashed `principal_key`,
`occurred_at`, `delivery_id`, `message_id`, inbox `sequence`, `device`, `provider`,
`status`, `reason`, `attempts`, provider HTTP `status_code`, `category`, `priority`
and `digest_id` when assigned. They do not contain inbox bodies, device tokens,
provider credentials or raw principal values. Use delivery ID to distinguish
multiple devices for one message.

For example, subscribe to `realtime.notification.failed` to mark a notification
as undeliverable or decide whether your application should attempt email fallback.
A sent event confirms provider acceptance, not display or receipt on the device;
use inbox acknowledgement events for application consumption.

One terminal transition produces one event per device delivery. Retryable
provider failures do not emit a terminal failed event while a retry remains
queued. Each digest member emits its own outcome with the common digest ID.
Pure metadata changes to an already-terminal row do not emit duplicate outcomes.
Cancelling an undrained fallback timer alone has no device delivery outcome;
its removal remains visible in the notification timeline. Existing terminal
records are not backfilled. Disabling or deleting a webhook follows the existing
outbox and subscription rules.

Apply `20261009070000001_managed_realtime_notification_webhooks.sql` after the
timeline migration. It has not been applied in this workspace.

### Channel snapshots

`PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/snapshot` accepts
`{"sequence":42,"data_base64":"...","binary":false}`. Your backend must ensure
that the state represents exactly the committed channel events through sequence
42. Gregale validates the sequence and stores state; it does not construct or
verify application state. The channel must already have retained history.

GET on the same path returns the state, sequence, `resume_after_sequence`,
`updated_at` and `expires_at`. Apply the state first, then subscribe with the
returned sequence as the replay baseline. The resume cursor is the existing
numeric after-sequence, not a new token. Events after it replay normally.
DELETE removes only the snapshot. Writes require existing deploy-write scopes;
reads require read scopes, with account ownership, MFA and the history preview gate.
Expose an application-authorized backend route to clients; do not put management
credentials in browsers.

Snapshots are limited to 64 KiB decoded and one per retained channel (at most
32 channels per endpoint). They expire 24 hours after replacement. A write cannot
move a snapshot backwards in sequence. GET returns 404 when absent and 410
`snapshot_unavailable` if expired or its replay tail has already been lost.
Publish fresh snapshots often enough to cover event retention and volume.
Retention can advance after GET; clients must still handle another resync error.
Snapshots do not extend event retention. Endpoint deletion removes snapshots.

The Go client provides Get/Put/DeleteManagedRealtimeChannelSnapshot. Node and
browser exports include `recoverRealtimeChannelSnapshot`: supply a loader through
your authorized backend and a callback that commits the decoded snapshot state.
The helper returns the baseline only after that callback completes; use it for
`initialSequence` or the consumer's `onResync` callback. Existing cursor stores
remain authoritative on normal reconnects.

Apply `20261009080000001_managed_realtime_channel_snapshots.sql` after the
notification-webhook migration. It has not been applied in this workspace.

### Atomic batch publishing

`POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish-batch`
accepts `{"batch_id":"job-123-completed","messages":[{"data_base64":"..."}]}`.
Supply 1–32 messages, at most 4096 decoded bytes each and 64 KiB total. Per-endpoint
message size limits also apply. Each message optionally sets `binary`.
Batch IDs are nonempty, at most 128 bytes, without whitespace edges or NUL/CR/LF.
The endpoint uses account ownership, MFA, deploy-write scopes and the history
preview gate. It publishes retained messages in one channel.

The full batch commits atomically with contiguous channel sequences. Invalid
requests or transaction failure publish no partial batch. The response contains
`batch_id`, `durable:true`, `partial` and ordered per-message publish outcomes.
Live fanout occurs after commit and is best effort; `partial:true` does not undo
the retained batch. Clients process ordinary ordered events and can replay them.
Atomicity applies to durable storage, not simultaneous client UI updates.

Reusing the same batch ID, channel and ordered content for 24 hours returns the
original sequence allocation without appending messages. Different content returns
409. Live delivery may be attempted again, so resume clients must retain their
normal sequence deduplication. Idempotency survives event eviction and edits;
the response confirms original publication, not that original data remains in the
current log. IDs can be reused after 24 hours. Up to 256 batch IDs are retained
per endpoint; new batches return 429 at capacity. Existing 1024-message/24-hour
channel retention still applies. Batch-created message IDs use a reserved derived
`batch:<SHA256(batch_id)>:<zero-based-index>` convention; avoid these IDs for
independent single publishes.

The Go client exposes `PublishManagedRealtimeChannelBatch`. Node/browser exports
include `publishRealtimeChannelBatch`, taking a stable ID, byte payloads and a
callback that submits its request through your authorized backend transport.
Do not expose management credentials to browsers.

Apply `20261009090000001_managed_realtime_atomic_batches.sql` after the snapshots
migration. It has not been applied in this workspace.

### Filtered retained-channel subscriptions

Retained channel publishers can attach `metadata`, for example
`{"event_type":"job.progress","project_id":"alpha"}`. Metadata is accepted on
retained publish, retained-message append and atomic batch messages. It supports
up to eight entries, lowercase ASCII keys matching `[a-z0-9_.-]{1,64}`, values up
to 128 UTF-8 bytes without NUL/CR/LF, and at most 4096 encoded JSON bytes. Metadata
participates in publication idempotency and batch hashes. Edits and deletion
events preserve the original labels.

A v2 client subscribes with an exact-match AND filter:

```json
{"type":"subscribe","channel":"jobs","after":0,"filter":{"project_id":"alpha"}}
```

Every filter entry must exist and match exactly. Empty or omitted filters match
all events. Missing metadata does not match a nonempty filter; existing messages
have empty metadata. Channel authorization runs normally before reading history.
Filters select events within an authorized channel; they do not grant access or
replace project-level authorization. Presence, transient signals and direct
messages are not filtered. Filtering applies to the retained-channel stream,
including replay and new events obtained by the live subscription pump.

Matching message frames expose metadata. A skipped event emits a `checkpoint`
frame containing channel and sequence, without its payload. Clients must durably
save that cursor and ACK it just as they do processed events, but must not call
application message handlers. The Node/browser SDK handles this automatically:
set `filter` on channel consumer options; received messages expose `metadata`.
Checkpoints preserve contiguous cursor advancement and avoid repeatedly scanning
skipped events after reconnect. Gaps in retained history still require resync.

Use a separate cursor store or durable subscription name for each filter. Changing
a filter while reusing a cursor will not replay previously skipped events. Use a
fresh cursor or explicit resynchronization to rebuild state for the new selection.
Channel snapshots are not automatically filtered; your backend must provide
application state appropriate for the selected scope.

Upgrade apid and realtimed together: the private history RPC now carries metadata.
Apply `20261009100000943_managed_realtime_subscription_filters.sql` after the batch
migration. It has not been applied in this workspace.

### Versioned channel event schemas

Register a contract with
`PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schemas/{event_type}/{version}`:

```json
{
  "schema": {
    "type": "object",
    "required": ["job_id", "progress"],
    "properties": {
      "job_id": {"type": "string", "minLength": 1},
      "progress": {"type": "integer", "minimum": 0, "maximum": 100}
    },
    "additionalProperties": false
  }
}
```

For example, register `job.progress` version `1`, then attach publisher metadata
`{"event_type":"job.progress","schema_version":"1"}` to a retained JSON event.
The version is an explicit canonical decimal string; it is stored and delivered
in the existing metadata, including batch messages. These two fields count toward
the eight metadata-entry limit. Unknown types or versions on an enforced channel
are rejected. Binary events cannot use JSON schemas.

Registering the first schema enables enforcement for all new retained events on
that channel. Publishers must then select a registered event type and version.
Prepare publishers to send those fields before enabling enforcement. Channels
without schemas retain their existing behavior, although an explicitly supplied
`schema_version` must resolve to a registered contract. Schemas may be registered
before the channel's first publication. Inbox notifications and live-only messages
are outside this channel-schema feature.

Validation runs inside the retained-write transaction, before allocating committed
sequences. A malformed event returns 400 with the offending JSON pointer and
validation reason. Batch errors also identify the zero-based `messages[index]`;
one invalid event rolls back the entire batch. Edits retain original schema
metadata and validate the new payload. Deletion tombstones are exempt. Historical
messages are not retroactively validated. Matching publication retries return
existing committed results without revalidating history.

Contracts use JSON Schema Draft 2020-12, limited to 16 KiB each. Local references
are supported; fetching external schema resources is disabled. Format keywords
use the validator's default annotation behavior. Each endpoint may register at
most 64 schema versions across its channels. Event types match
`[a-z0-9_.-]{1,64}` and versions range from 1 to 1000000. Versions are immutable:
identical PUTs succeed, changed contracts return 409. Register a new version for
format changes. No version deletion or enforcement-disable endpoint is provided;
existing event contracts remain available while clients migrate.

GET on the registration path returns the contract and creation time. Registration
requires account ownership, MFA, deploy-write scopes and the history preview gate;
reads require read scopes. The Go client exposes Put/GetManagedRealtimeEventSchema.
Node/browser exports include `realtimeEventSchemaMetadata` to select a version
without manually formatting its metadata fields.

Apply `20261009110000001_managed_realtime_event_schemas.sql` after the subscription
filter migration. It has not been applied in this workspace.

### Built-in channel state reducers

Enable a reducer with
`PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/reducer`:

```json
{"sequence":0,"entities":{}}
```

This creates an empty retained channel if necessary, within the existing
32-channel limit. For an existing channel, provide a seed entity map that your
backend knows represents its current sequence. The sequence must exactly match
the committed channel head; stale baselines return 409. Gregale does not reconstruct
seed state from historical events. An active reducer cannot be reseeded; an
identical PUT at its current state is repeatable.

Once enabled, publish JSON operation events through retained publishing or atomic
batches:

```json
{"op":"set","key":"job-123","value":{"progress":0,"status":"running"}}
{"op":"merge","key":"job-123","value":{"progress":75}}
{"op":"delete","key":"job-123"}
```

`set` replaces one entity with an object. `merge` overwrites the supplied top-level
fields, creating the entity if absent; nested objects are replaced, not recursively
merged, and null remains a value. `delete` removes an entity and is valid even when
it is absent. Keys accept 1–128 UTF-8 bytes without whitespace edges or NUL/CR/LF.
`increment` atomically adds a safe-integer delta to a top-level numeric field
(see counter operations below). `append` and `remove` update top-level arrays
(see array operations below). No custom reducer code runs on the server.
Schema-enabled channels still require schema metadata and matching operation
payloads. Binary events and malformed operations are rejected.

Reduced state and event publication commit together. A batch applies operations
in order and commits only if every step succeeds. State is bounded to 128 entities
and 64 KiB encoded JSON, including at intermediate batch steps. Each individual
event remains subject to the 4 KiB limit. Invalid operations or capacity violations
reject publication without committing events or consuming sequences. Matching
publication retries do not apply operations twice.

GET on the reducer path returns `entities`, `sequence` and `updated_at`. The normal
channel snapshot GET also returns this state as JSON bytes with the exact resume
sequence, so existing snapshot recovery helpers can load it and replay subsequent
events. Reduced state persists while idle; its snapshot read offers a fresh
24-hour expiration even without new events. Snapshots do not extend event retention,
and clients must still handle retention advancing after a read. Returned state
contains all channel entities; subscription filters do not filter it.

Active reducers own channel snapshots. Manual snapshot writes/deletes and retained
message edits/deletes return 409; publish new compensating operations instead.
DELETE on the reducer path disables it and removes managed state and its generated
snapshot, leaving retained events intact. A later enable needs a new baseline.
Endpoint deletion removes managed state.

Reducer management uses account ownership, MFA and the history preview gate, with
read scopes for GET and deploy-write scopes for PUT/DELETE. The Go client exposes
Put/Get/DeleteManagedRealtimeReducer. Node/browser exports include
`realtimeReducerEvent` to encode operation bytes for retained publishing or batches.

Apply `20261009120000001_managed_realtime_channel_reducers.sql` after the event-schema
migration. It has not been applied in this workspace.

### Optimistic concurrency for retained publishing

Retained channel publishes and retained-message appends accept optional
`expected_sequence`. Atomic batch requests accept it at the top level, applying
to the channel head before the entire batch. It is not supported on individual
batch items, live-only publishes, direct messages or inbox notifications.

For example, after reading reducer state at sequence 42, publish a conditional
update with `expected_sequence: 42`. The first successful writer advances the
head; another writer using 42 receives 409 before any event or reducer state
change commits. A batch that matches 42 allocates sequences 43 onward atomically.
The check uses the existing channel serialization lock, across replicas and
concurrent single/batch publishers.

A conflict returns problem code `realtime_sequence_conflict`, including
`expected_sequence` and `current_sequence`. The current value is the head observed
at the failed check and may advance afterward. Reload current state or a fresh
snapshot, reconcile the intended update, and retry with that state's sequence.
The precondition is channel-wide, not scoped to a reducer entity or subscription
filter. Every committed event advances it, including unrelated entity updates.

Omit the field or use null for an unconditional publish. Explicit zero requires a
channel with no committed events, including a newly created channel. Values must
be nonnegative integers. Matching idempotent message or batch retries return their
original committed result even if the channel has since advanced; the precondition
only applies to new writes, and is not part of payload identity. Changed payload
or metadata under an existing key remains an idempotency conflict. Existing
retention and batch-ID lifetime limits still apply.

Go request types expose `ExpectedSequence *int64`, preserving the distinction
between omission and zero. `PublishManagedRealtimeChannelAtSequence` explicitly
publishes a retained event with a precondition and stable idempotency key. Existing
batch and retained-append client methods accept the same request field.
Node/browser exports include `realtimeExpectedSequence` for request bodies, and
`publishRealtimeChannelBatch` accepts a fourth argument
`{ expectedSequence: 42 }`. SDK problem envelopes expose the conflict sequences.

No additional migration is required; the check uses the existing channel head.

### Per-entity optimistic concurrency

Reducer events accept an optional `expected_version` inside their JSON payload:

```json
{"op":"merge","key":"job-123","value":{"progress":75},"expected_version":4}
```

Reducer state responses expose `entity_versions`, a map separate from `entities`.
Reducer snapshots expose the same map alongside their existing base64 entity data.
Seed entities start at version 1; an unseen key has version 0. Every accepted
set, merge, increment, append, remove, or delete increments that key's version, including operations that
leave its value unchanged. Omit `expected_version` for an unconditional update.
Versions must be nonnegative JavaScript-safe integers; counters cannot overflow.

Version checks and state changes share the retained-publish transaction. Batch
operations observe earlier operations in that batch, and any mismatch rejects
the whole batch. Matching idempotent retries return the original result without
applying the operation again. Other entities' updates do not affect this check;
`expected_sequence` can additionally guard the entire channel.

A mismatch returns HTTP 409 with code `realtime_entity_version_conflict`,
`entity_key`, `expected_version`, `current_version`, `entity_exists`, and a
zero-based `message_index` (0 for a single publish).

Deletion retains a version tombstone, preventing stale writers from recreating
an entity. There are at most 128 live entities and 256 tracked keys, including
tombstones. New keys are rejected when the tracked-key limit is reached; existing
keys remain usable. Disabling and reseeding a reducer resets all entity versions,
so coordinate this administrative reset with writers. Schema-validated channels
must register a schema that permits `expected_version` before using it.

The entity-version migration must be applied before running this server version.
It initializes existing reducer entities at version 1; it cannot recover historic
per-entity versions or deletions from before this feature.

### Reducer entity expiration

Set and merge events accept `expires_at`, a future RFC3339 timestamp no more
than 30 days after the server accepts the event:

```json
{"op":"set","key":"typing:user-123","value":{"active":true},"expires_at":"2026-10-08T15:30:10Z"}
```

A set without a deadline clears any previous deadline. A merge without the
field preserves the current deadline; use `expires_at: null` to clear it, or a
new timestamp to refresh it. Delete events cannot include `expires_at`. Seed
entities have no deadlines. Invalid deadlines reject the whole batch. Matching
idempotent retries preserve their original deadline and do not refresh it.

Reducer state and snapshots optionally return `entity_expirations`, a map of
entity keys to scheduled timestamps. Snapshot data remains the bare entity map.
Expiration is asynchronous: state reads may still contain an overdue entity
until its delete event commits. Clients should use the retained event as the
authoritative removal, rather than silently changing their replay cursor.

With retained-history preview enabled, apid processes up to 128 due entities
per pass on startup and every five seconds. Deadlines survive PostgreSQL server
restarts. Backlogs or outages delay cleanup; this is not a precise timer. Each
candidate's version and deadline are rechecked under the channel lock, so
concurrent refreshes, deletes, and multiple apid workers cannot commit a stale
or duplicate expiration. Expiration advances the entity version and keeps the
normal deletion tombstone. Disable/reseed remains an administrative version
reset and must be coordinated with writers.

The server appends this event through the normal retained history and reducer
transaction, and then attempts live fanout:

```json
{"op":"delete","key":"typing:user-123","expected_version":2,"reason":"expired"}
```

Generated events carry metadata `event_type: reducer.expired` and bypass customer
JSON Schema validation. Customer publishes continue to undergo normal schema
checks, including when they claim that event type. Configure metadata filters
to include expiration events when maintaining a filtered entity view. Live
fanout is best effort; replay recovers events missed during a node outage or a
crash after commit. Apply the entity-expiration migration before deploying this
server version; it initializes existing entities without deadlines.

### Atomic counter operations

Publish an `increment` reducer event to add a delta to a top-level entity field:

```json
{"op":"increment","key":"post-123","field":"likes","delta":1,"min":0,"max":1000000}
```

Missing entities or fields start at zero. A field name is a literal object key,
not a nested path, and uses the same 1–128 byte validation as entity keys.
Positive, negative, and zero deltas are allowed. A successful zero delta still
advances the entity version. Existing nulls, strings, booleans, objects, fractional
numbers, and out-of-range integers are rejected; values are never coerced.

Counters, deltas, optional `min`/`max`, and results must be integers within
±9,007,199,254,740,991. Bounds are inclusive and checked against the result, not
the initial value. Exceeding a bound rejects the operation instead of clamping
it. Omit bounds to use the safe-integer range; null bounds are invalid. Numeric
JSON literals are parsed exactly; integral decimal/exponent forms are accepted,
with a 128-character literal limit and exponents limited to ±128.

Counter updates share the retained event/state transaction and entity-version
checks. Batches see earlier counter changes and roll back entirely on a bad
counter, bound violation, or version conflict. Matching publication retries do
not increment again. Invalid counters return 400 `realtime_invalid`; version
mismatches retain the existing 409 conflict response. The ordinary entity-count,
state-size, and event-size limits still apply.

`expected_version` is optional. `expires_at` behaves like merge: omission preserves
the deadline, a timestamp replaces it, and null clears it. Incrementing changes
the version, invalidating older expiration candidates even when the deadline is
preserved. Schema-enabled channels must register a compatible increment schema
before publishing this operation. No migration is needed for counter operations.

### Atomic array operations

Append or remove JSON values from a top-level array field with retained events:

```json
{"op":"append","key":"room-123","field":"members","items":["user-1","user-2"],"unique":true,"max_length":100}
{"op":"remove","key":"room-123","field":"members","items":["user-1"]}
```

`items` must contain 1–32 JSON values. Fields are literal object keys, not paths,
and follow the same 1–128 UTF-8 byte rules as entity keys. Missing entities or
fields start as empty arrays; both operations create that array when absent.
Existing nulls, objects, strings, and other non-arrays are rejected.

Append preserves order and adds every supplied item by default. `unique: true`
skips items equal to any existing item or an earlier appended item; it does not
remove duplicates already in the array. Remove deletes every occurrence of any
matching supplied value and preserves the order of the remaining items. `unique`
is valid only on append and must be a boolean.

Equality is structural JSON equality: object key order does not matter, nested
arrays remain order-sensitive, and numbers compare exactly by numeric value
(`1`, `1.0`, and `1e0` match, as do `0` and `-0`). Types remain distinct: the string
`"1"` does not match the number `1`. Large numeric literals do not pass through
floating-point conversion during comparison.

Each operation accepts optional `max_length`, an integer from 0 to 256 (default
256). It limits the resulting array for that operation; it is not a persistent
field constraint. Exceeding the limit rejects the operation instead of truncating.
Existing arrays longer than 256 items must first be replaced with set/merge.
The reducer's 64 KiB state limit and 4 KiB event limit also apply.

Array operations use the existing atomic retained/state transaction. Batches
observe earlier operations and roll back entirely on any invalid array, length
violation, or version mismatch. Matching idempotent retries never append twice.
Successful no-op removals or unique appends still advance the entity version.
`expected_version` works normally. `expires_at` follows merge/increment behavior:
omission preserves the deadline, a timestamp replaces it, and null clears it.
Schema-enabled channels must register compatible schemas before publishing these
operations. No migration is needed.

### Atomic field conditions

Every reducer operation accepts an optional `conditions` array. All predicates
must hold on the target entity immediately before applying the operation:

```json
{"op":"merge","key":"job-123","value":{"status":"running","worker":"worker-1"},"conditions":[{"field":"status","equals":"queued"},{"field":"worker","absent":true}]}
```

Each predicate has a literal top-level `field` and exactly one of `equals` (any
JSON value, including null) or `absent: true`. Field names follow the existing
1–128 UTF-8 byte key rules. Conditions are ANDed and limited to 1–16 predicates;
omit the field for an unconditional operation. Empty/null arrays, unknown
predicate properties, missing fields, both predicate kinds together, and
`absent: false` or null are invalid. Duplicate field predicates are allowed and
all must pass.

Equality follows array-operation structural equality: object key order is
irrelevant, arrays remain ordered, and numbers compare exactly by value.
`equals: null` requires an existing null field; it does not match a missing
field. An absent predicate passes on a missing entity. Equality predicates fail
on missing entities or fields. Conditions apply to the state before set replaces
it or delete removes it, and before counter/array updates change their fields.

Checks share the channel's retained/state transaction. Batch predicates observe
earlier operations in the batch. A failed predicate rejects the whole batch,
including earlier writes, without consuming sequences or changing versions or
deadlines. Optional `expected_version` is checked first; a stale version returns
the existing version-conflict response. Matching idempotent retries return the
original committed result without rechecking conditions against later state.

A failed condition returns HTTP 409 `realtime_condition_conflict`, with
`entity_key`, `condition_field`, zero-based `condition_index` and `message_index`
(0 for a single publish), `current_version`, `entity_exists`, and `field_exists`.
The first failed predicate is reported; actual field values are not included.
Malformed predicates return 400 `realtime_invalid`. Schema-enabled channels need
schemas that permit the new `conditions` field. No migration is needed.

### Scheduled retained publishing

Use a schedule ID as an idempotency key to queue a channel event for a future time:

```http
PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/reminder-123
Content-Type: application/json

{"data_base64":"eyJraW5kIjoicmVtaW5kZXIifQ==","binary":false,"deliver_at":"2026-10-09T12:00:00Z"}
```

Payloads are at most 4 KiB and accept the existing optional metadata map.
`deliver_at` must be in the future and within 30 days; deadlines normalize to
UTC microsecond precision. Creating a schedule does not reserve a channel history
sequence or consume a channel slot. An endpoint holds at most 256 pending and
recent terminal schedules. Schedule IDs follow the 1–128 UTF-8 byte key rules
and are scoped to endpoint/channel.

GET `/channels/{channel}/schedules` lists pending schedules and terminal records
from the last 24 hours, ordered by next attempt time (or delivery time) then ID. Responses include the
payload, metadata, deadline, version, status, created/updated timestamps, and a
history `sequence` once published. Statuses are `pending`, `published`, `canceled`,
and `failed`. A failed record includes a machine code in `last_error`.

An identical PUT with the same ID, deadline, bytes, binary flag, and metadata
returns the existing record, including after delivery or cancellation. Different
contents conflict. This retry guarantee lasts while the record is retained;
terminal IDs can be reused after 24 hours. Rescheduling changes the stored
deadline, so a retry of the original PUT then conflicts. Use a distinct schedule
ID when creating a different logical event.

PATCH `/channels/{channel}/schedules/{schedule_id}` with
`{"deliver_at":"...","expected_version":1}` reschedules a pending event.
DELETE that path with `?expected_version=1` cancels it. Both serialize with
publishing and require the current positive version; changing a pending schedule
increments its version. Canceling an already canceled record is an idempotent
success. Once delivery commits, rescheduling and cancellation return 409. A
PATCH retry after its first successful change needs a fresh version from listing;
it is not an idempotent replacement.

With retained-history preview enabled, apid runs an immediate pass and processes
up to 128 due schedules every five seconds. PostgreSQL preserves schedules
across restarts. Cleanup can lag during backlogs or outages; timestamps are not
precise delivery guarantees. Schedule revision and status are rechecked under
lock. History append, reducer update, and `published` status commit in one
transaction, preventing duplicate publication by multiple workers. Cancel or
reschedule racing with delivery takes effect only if it commits first.

Delivery applies the endpoint's current payload-size policy and normal event
schema/reducer validation. Version conditions and entity deadlines embedded in
a reducer payload are evaluated at delivery time. Bad schemas, stale entity
versions, failed field conditions, invalid reducer operations, payload-policy
violations, or exhausted channel capacity mark the schedule `failed`; no history
event commits. Failed schedules can be retried with their existing contents (see retry controls
below); create a new ID to change the payload. Delivery failures follow the
configured retry policy.

After commit, live fanout is best effort. A worker crash or node outage can miss
live delivery; subscription replay recovers the committed event. Live fanout
failure does not cause another history append. The schedule retains its original
metadata, so the normal subscription filters apply.

Management uses the history preview gate, account ownership, MFA, read scopes
for listing, and deploy-write scopes for mutations. Endpoint deletion removes
its schedules. Terminal storage is bounded by endpoint capacity; PostgreSQL
prunes old terminal rows when creating schedules. Apply the schedule migration
before running this server version. Go client methods cover create/list,
reschedule/cancel; Node/browser export `realtimeScheduledEvent` and schedule types
for use with an authorized backend transport.

### Schedule retry controls

Creation accepts `max_attempts` (1–10, default 1) and `backoff_seconds` (5–3600,
default 5). The attempt limit includes the initial delivery; 1 disables automatic
retries. With a larger limit, recorded delivery failures keep the schedule pending
and set `next_attempt_at`. Delays double from the configured base after each
failure, capped at one hour. The final failure marks the schedule `failed` and
clears its next attempt. Policy is immutable after creation and included in PUT
idempotency comparisons. Existing schedules default to one attempt after migration.

```json
{"data_base64":"eyJraW5kIjoicmVtaW5kZXIifQ==","deliver_at":"2026-10-09T12:00:00Z","max_attempts":5,"backoff_seconds":30}
```

Retry a failed schedule without changing its ID or payload:

```http
POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/reminder-123/retry
Content-Type: application/json

{"expected_version":6}
```

Omit `deliver_at` to retry on the next worker pass, or supply a future timestamp
within 30 days. A manual retry requires the current schedule version, advances
that version, and starts a fresh attempt cycle using the original retry policy.
Only failed schedules can retry; published, canceled, and already-pending records
return 409. A repeated manual retry request also conflicts after its first success;
list the schedule to recover its latest status. No payload or metadata is changed,
and the original `deliver_at` stays intact. Expired terminal receipts are not retryable.

Responses expose `attempts` (lifetime recorded attempts), `cycle_attempts`
(attempts since creation or the latest manual retry), `max_attempts`,
`backoff_seconds`, optional `last_attempt_at`, and optional `next_attempt_at`.
Success and durably recorded failure count as attempts and advance the schedule
version. `last_error` retains the most recent failure code even after a successful
retry, so use `status` to determine the current outcome. Concurrent failures for
the same candidate count once, using the schedule's version check under lock.
Attempt counters cannot account for storage failures that prevent recording an
outcome, process crashes before recording, or canceled worker contexts.

All recorded publication failures use the policy, including schema errors,
reducer version/field conflicts, invalid events, capacity/payload-policy errors,
and storage failures (`realtime_schedule_unavailable`). Automatic retries reuse
the original event: stale expected versions or expired payload deadlines need a
new corrected schedule rather than more retries. Worker shutdown/timeouts leave
unrecorded candidates pending. The worker still runs in bounded five-second
passes, so retries can be later than their advertised next attempt time.

Normal cancellation stops pending automatic or manual retries and clears
`next_attempt_at`; use the latest schedule version. Rescheduling a pending retry
changes `deliver_at` and clears its next attempt timestamp but preserves the
consumed cycle budget. History commit and published status remain atomic. Neither
live fanout failures nor manual retries can republish an already-published record;
clients recover missed delivery through retained replay.

Retry management uses the same preview, ownership, MFA, and deploy-write scope
checks as schedule creation. Apply the schedule-retry migration before running
this server version. It backfills one recorded attempt for existing published
or failed schedules; detailed earlier attempt histories cannot be reconstructed.

### Schedule delivery history

GET `/channels/{channel}/schedules/{schedule_id}/history` returns a timeline of
committed schedule changes. Optional `after_version` defaults to 0; `limit`
defaults to 50 and accepts 1–100. Entries are ordered by schedule version.

```json
{"schedule_id":"reminder-123","channel":"updates","oldest_version":1,"latest_version":3,"history_truncated":false,"has_more":false,"events":[{"version":1,"event":"created","status":"pending","attempts":0,"cycle_attempts":0,"deliver_at":"2026-10-09T12:00:00Z","occurred_at":"2026-10-08T12:00:00Z"},{"version":2,"event":"attempt_failed","status":"pending","attempts":1,"cycle_attempts":1,"deliver_at":"2026-10-09T12:00:00Z","next_attempt_at":"2026-10-09T12:00:30Z","failure_code":"realtime_condition_conflict","occurred_at":"2026-10-09T12:00:00Z"},{"version":3,"event":"published","status":"published","attempts":2,"cycle_attempts":2,"deliver_at":"2026-10-09T12:00:00Z","sequence":42,"occurred_at":"2026-10-09T12:00:30Z"}]}
```

Event kinds are `created`, `rescheduled`, `canceled`, `manual_retry`,
`attempt_failed`, and `published`. Failures include their machine code and next
attempt time when automatic retry remains pending; publication includes the
committed channel sequence. Every entry also records status, lifetime and cycle
attempt counts, the delivery deadline, and occurrence time. Payloads and metadata
are not copied into history. Idempotent creation/cancellation retries and stale
worker candidates do not add entries.

History and schedule changes commit atomically. Successful publication, reducer
changes, history append, and its timeline entry share one transaction. Reads use
a consistent snapshot of the schedule and timeline. Events describe recorded
outcomes, not precise attempt start times; worker crashes, storage failures that
prevent recording, and live fanout outcomes are not timeline events.

Each schedule retains its latest 128 entries. `oldest_version` identifies the
first available event; `latest_version` identifies the schedule revision in the
read snapshot. `history_truncated` indicates unavailable earlier history. Fetch
subsequent pages using the last returned event's version until `has_more` is
false. Pages can change between requests; if a cursor falls behind retained
history, compare it with `oldest_version` to detect missing entries. Truncation is
reported rather than returning a misleading complete timeline.

The migration seeds existing schedules with a `baseline` snapshot at their
current revision. Baselines do not reconstruct earlier attempts and set
`history_truncated`, even at version 1. Apply this migration before running the
updated server. Terminal history expires with the schedule's 24-hour receipt;
expired/missing schedules return 404. Schedule ID reuse starts a fresh timeline,
and endpoint deletion removes its history. History reads use the same preview,
ownership, MFA, and read scopes as listing schedules.

The Go client provides `GetManagedRealtimeScheduleHistory`; Node/browser export
`RealtimeScheduleHistory` and `RealtimeScheduleHistoryEvent` for authorized
backend transports.

### Recurring retained events

Create a recurring schedule with `interval_seconds` (5–2,592,000 seconds), optional
`max_occurrences` (1–1,000,000; omit or use 0 for unlimited), and/or `end_at`:

```json
{"data_base64":"eyJraW5kIjoicmVmcmVzaCJ9","deliver_at":"2026-10-09T12:00:00Z","interval_seconds":60,"max_occurrences":100,"max_attempts":3,"backoff_seconds":5}
```

The first occurrence uses `deliver_at`. After successful publication, the next
occurrence is planned one interval after that commit's event timestamp. This is
fixed-delay recurrence, not wall-clock/cron scheduling: retries and outages shift
later occurrences and do not produce catch-up bursts. `end_at` must fall between
the initial deadline and one year later. It limits planned occurrence times;
a due occurrence and its retries can finish later during a backlog. No new
occurrence is planned after the end time or occurrence limit (successful or intentionally skipped slots).

Responses add `initial_deliver_at`, a one-based `occurrence`, and
`completed_occurrences` (successful recurring publications). While another
occurrence remains, status stays `pending`, `deliver_at` moves to its next planned
time, and the retry cycle budget resets to zero. When recurrence completes,
status is `published`. `sequence` is the latest successfully published channel
sequence, including while the series has another pending occurrence. All
occurrences reuse the stored payload and base metadata. Idempotent creation
retries for a recurring ID compare the original initial deadline and policy,
even after the series advances.

Recurring schedules reserve two message metadata fields: `schedule_id` and
`schedule_occurrence`. Supply at most six other metadata entries and do not use
those reserved names. The endpoint/channel plus these fields identify one logical
occurrence. Workers recheck occurrence and version under lock; publication,
reducer state, next occurrence, and timeline entry commit together, preventing
duplicate publication. Retries preserve the same occurrence identity. Timeline
entries include occurrence and completed-count fields; a `published` entry
records the occurrence that finished, even when current schedule state already
points to the next one.

Each occurrence uses the configured automatic retry policy independently. If it
exhausts retries, the series becomes `failed` without advancing to another
occurrence. Manual retry restarts that failed occurrence's budget. After eventual
success, recurrence continues if limits permit. Live fanout failures never retry
an already committed occurrence; replay recovers its event.

POST `/channels/{channel}/schedules/{schedule_id}/pause` or `/resume` with
`{"expected_version":...}` to suspend or restart a recurring series. Pause
requires a pending series; resume requires a paused one and cannot run after its
end time. Both advance the version and record timeline entries. Pause preserves
the planned deadline and retry budget; resume makes an overdue occurrence
eligible for the next worker pass, followed by the ordinary fixed delay. These
controls serialize with publishing; a due commit that wins the race completes
before the pause applies to the new current occurrence.

Cancel uses the existing DELETE API and current version, and works for pending
or paused series. Rescheduling remains available for pending occurrences,
changes their planned time, preserves their retry budget, and cannot set a time
after `end_at`. Paused series remain stored and count against endpoint schedule
capacity until resumed or canceled; they do not expire after 24 hours. Terminal
receipts and history retain the existing 24-hour rules. Recurrence controls use
ownership, MFA, deploy-write scopes, and the retained-history preview gate.

Apply the recurrence migration before running this server version. Existing
schedules remain one-time events with occurrence 1. The Go client exposes
`SetManagedRealtimeSchedulePaused`; Node/browser schedule helpers accept
`intervalSeconds`, `maxOccurrences`, and `endAt`.

### Conditional schedule execution

Schedules can evaluate conditions against the built-in reducer state of their
own channel before publishing. Conditions are separate from the payload, so they
can check other entities as well as the entity targeted by a reducer operation:

```json
{"data_base64":"...","deliver_at":"2026-10-09T12:00:00Z","conditions":[{"key":"job-123","exists":true},{"key":"job-123","field":"status","equals":"pending"}],"on_condition_failure":"skip"}
```

Supply 1–16 AND predicates within 4 KiB encoded JSON. Each has `key` and exactly
one of `exists` (boolean), `equals` (any JSON value), or `lt`/`lte`/`gt`/`gte`
(safe-integer threshold). Existence applies to an entity and omits `field`;
other operators require a literal top-level field. Keys and field names follow
the existing 1–128 UTF-8 byte rules. Equality uses structural JSON comparison,
with exact numbers. Missing fields never match `equals`, including null. Numeric
comparisons require the current value and threshold to be safe integers.

The channel must have an active reducer. Missing reducer state fails the
condition rather than treating it as an empty state that could accidentally
pass `exists: false`. Checks run under the same channel lock and transaction as
retained publication. A passing event still undergoes normal schema, payload,
and reducer validation; conditional scheduling does not allow arbitrary
notification payloads on a reducer-owned channel. Cross-channel conditions are
not supported.

`on_condition_failure` defaults to `retry` when conditions are supplied. A failed
check records `realtime_schedule_condition_failed` and follows the configured
retry budget/backoff. The default one-attempt policy ends as failed; automatic
retry requires `max_attempts > 1`. Conditions are re-evaluated at every attempt.

With `skip`, a failed check commits a skipped occurrence and history entry,
without appending a channel event or advancing its message sequence. One-time
schedules end with status `skipped`; recurring schedules advance to the next
fixed-delay occurrence while limits allow. If the series ends on a skip, its
terminal status is `skipped`. Skips count toward `max_occurrences`, avoiding an
unbounded series of false predicates. `completed_occurrences` still counts
successful publications; `skipped_occurrences` counts intentional skips.

Responses include the latest `skip_reason`, and skipped history entries include
that reason, occurrence identity, counts, and the failure code. Reasons identify
the predicate index and one of `reducer_missing`, `entity_existence_mismatch`,
`entity_missing`, `field_missing`, `field_not_integer`, `value_mismatch`, or
`threshold_not_met`; actual field values are not exposed. A skipped evaluation
counts as a recorded attempt and advances the schedule version. Skipped terminal
records cannot be manually retried; create a new ID to reevaluate them. Retry-mode
failed records use the ordinary manual retry API.

Conditions and failure behavior are immutable creation inputs and included in
idempotency comparisons. No condition data is added to published event payloads.
Existing scopes, ownership, preview gating, history limits, and terminal receipt
retention apply. Apply the conditional-schedule migration before running this
server version; existing schedules remain unconditional.

### Schedule completion webhooks

App webhook subscriptions accept `realtime.schedule.published`,
`realtime.schedule.failed`, and `realtime.schedule.skipped`. Subscribe using the
existing app webhook API and event filter; an empty filter includes these events.
Only enabled app-scoped subscriptions for the endpoint's app receive them.

Every published or intentionally skipped occurrence emits its own outcome, even
when a recurring schedule advances to another pending occurrence. Failure emits
only when the occurrence exhausts its automatic retry budget. A later manual
schedule retry can produce another outcome. Pause, resume, cancellation, and
intermediate failed attempts do not emit completion events.

The payload includes `event_id`, app/endpoint/channel/schedule identity, version,
occurrence, completion/skip counts, outcome, attempts, planned deadline, and
outcome timestamp. Successful publication includes `sequence`; failed and skipped
outcomes include `failure_code`, and skips include `skip_reason`. Original event
data and reducer values are omitted. JSON delivery uses the envelope's `payload`;
CloudEvents delivery uses `data`.

Recipient IDs and the event are written to the webhook outbox in the same
transaction as the schedule outcome and history. The existing dispatcher signs
requests and applies the subscription's retry/backoff policy. Delivery is at
least once: verify the signature over the exact raw body, then persist
`X-Faas-Delivery-Id` with receiver side effects for deduplication. `event_id`
identifies the outcome across subscriptions; delivery IDs identify each recipient's
delivery. Do not assume delivery order.

Inspect deliveries with GET `/v1/apps/{slug}/webhooks/{id}/deliveries`, and attempt
history with GET `/v1/apps/{slug}/webhooks/{id}/deliveries/{did}/attempts`.
POST `/v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry` retries a dead delivery.
Apply the schedule-webhook migration before enabling this server version.

### Schedule groups and bulk controls

Create a schedule with an optional immutable `group`, such as `campaign-123`.
Labels are channel-scoped, 1–128 UTF-8 bytes, without surrounding whitespace,
NUL, CR, or LF. The label participates in creation idempotency; changing it on
an existing ID conflicts. It is management data and is not added to event payloads.

GET the channel's `/schedules?group=campaign-123` to list a group. Optional
`status` accepts pending, paused, published, failed, skipped, or canceled.
Responses include `totals` over the filtered retained rows: each status count,
plus completed and skipped occurrence counts. Successful one-time schedules
count as one completed occurrence. These are not lifetime analytics; terminal
receipts retain the existing 24-hour window, and recurring status describes the
current series while occurrence counts describe its progress.

POST `/schedules/groups/{group}/pause`, `/resume`, or `/cancel` with:

```json
{"expected_versions":{"schedule-a":3,"schedule-b":7}}
```

Include exactly every pending or paused member's ID and current version from a
fresh, unfiltered-by-status group list. Terminal members are excluded. Missing,
extra, or stale members return 409 without any mutation. All transitions and
history entries commit atomically. Schedule creation serializes with membership
capture, and workers invalidate stale candidates through existing version checks.
Both one-time and recurring members can be paused/resumed through this API.
Members already in the requested state remain unchanged. Empty groups accept an
empty map. No group label resource is created or retained separately.

Pause/resume preserves the deadline and retry budget; overdue resumed events are
due on the next worker pass. Resuming a paused member after its recurrence end
bound conflicts for the whole action. Cancellation clears pending retry deadlines.
Group actions do not emit completion webhooks. Existing app ownership, MFA,
deploy-write scopes, preview gating, and endpoint capacity apply. Apply the
schedule-group migration before running this server version.

### Backend ephemeral signals

Client-sent signals already use `sendSignal`, `sendTemporarySignal`, and
`onSignal`. Backends can now POST
`/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/signals`:

```json
{"data":{"active":true},"name":"typing","ttl_ms":5000}
```

The API requires existing app ownership, MFA, and deploy-write scopes. It uses
the v2 signal delivery path with `member_id: "backend"`, and does not append
history, advance a sequence, invoke reducers, or create replay/idempotency
receipts. V1 connections do not receive signal frames. Only currently connected
v2 channel subscribers can receive them through the existing `onSignal` callback.

`data` can be any JSON value, including null, within 2048 encoded UTF-8 bytes and
the endpoint's payload limit. The entire request is limited to 4096 bytes.
Optional names use 1–64 ASCII letters, digits, underscore, dot, colon, or hyphen.
Named signals default to 5000 ms TTL; explicit `ttl_ms` accepts 0–30000 and requires
a name. Use null data and TTL 0 to clear a named signal. All backend publishers
share the backend sender identity in a channel; use distinct names for independent
activities. Expiry timestamps are assigned by the API server.

Backend publishing is limited to 20 requests per second per endpoint/channel on
each API process, separately from client signal limits and normal auth limits.
429 responses include `Retry-After: 1`; the limiter also bounds active bucket
memory and may reject creation of additional buckets at capacity. This is a
process-local limit, not a fleet-wide quota.

A 200 response contains `accepted`, `member_id`, and an optional `expires_at`.
Acceptance means best-effort fanout completed, including an empty subscriber set;
it is not a subscriber acknowledgement. Slow subscribers may lose signals, and
routing failures may follow partial delivery. Signals have no delivery retry or
reconnect replay guarantee. Receivers should apply expiry for temporary activity
and avoid using signals for durable business actions. No migration is needed.

### Coalescing named signals

Each connection's outbound queue retains one pending signal for a given channel,
sender member ID, and name. A newer queued update replaces that payload in place,
including a zero-TTL clear. Older timestamps do not replace newer pending values.
The queue slot keeps its original position; coalescing does not impose ordering
relative to retained events or signals already being written. Different names,
senders, and channels remain independent; unnamed signals retain normal delivery.

Replacement adjusts the existing outbound byte budget rather than allocating
another queue slot. A larger replacement that exceeds the byte budget follows
existing queue-full behavior. Coalescing occurs on each receiving node and does
not bypass publisher rate limits. Named signals that expire while waiting are
discarded before the socket write; zero-TTL clears are still delivered. A frame
already being written cannot be replaced, and expiry may occur during transmission.
Clients should still honor expiry timestamps. Disconnect cleanup releases both
queue slots and their latest payload accounting.

The Node/browser SDK exports `createRealtimeSignalCoalescer` for high-frequency
cursor or activity updates. It sends the first value immediately and only the
latest pending value at the end of each throttle interval. Defaults are 100 ms
interval and 5000 ms TTL. Intervals accept 50–30000 ms; TTL accepts 1–30000 ms.
`flush()` sends the final pending value immediately; `dispose()` flushes and stops,
while `dispose(false)` or `cancel()` discards pending activity. Manual flushes can
exceed the throttle rate, so server limits still apply. Values are snapshotted as
JSON when submitted and validated against the existing 2 KiB signal limit.

Timer-send failures call the required `onError` handler and retain the latest
pending value for explicit retry/flush. Direct sends and flushes throw to their
caller. There is no automatic reconnect replay; recreate or reconnect the sender
through the existing channel actions. Use `dispose(false)` on channel teardown
when the final value is no longer useful. No migration or new API is required.

### SDK named activity tracker

`createRealtimeActivityTracker({ onChange, maxEntries? })` maintains the latest
named signals per channel, sender, and name. Feed existing `onSignal` events into
`apply`; render the isolated activity list from `onChange` or `snapshot()`.
The tracker ignores unnamed signals, duplicate timestamps, and stale updates,
removes explicit clears immediately, and removes expired activity automatically.
A newer already-expired update also removes older activity instead of extending
it. Server timestamp fractions are retained when comparing revisions.

One timer services expiry and short-lived ordering markers. These markers prevent
delayed pre-clear updates from restoring activity. Updates outside a 30-second
past/future clock window are ignored. Expiry follows the server's timestamp using
the client's clock, so clock alignment matters. Data follows the existing 2 KiB
JSON limit, and snapshots clone data to keep UI mutations isolated.

Capacity defaults to 1024 identities including ordering markers; configure
`maxEntries` from 1 to 8192. Additional identities throw rather than silently
evicting active activity. Reset a channel on unsubscribe/reconnect to clear its
activity and ordering memory; signals do not replay across reconnects. `dispose`
stops timers and clears state, notifying the UI when visible activity existed.
Callbacks must handle their own UI errors; exceptions propagate, including from
expiry timers. This is a local SDK convenience and requires no server migration.

### Activity lifecycle integration

Channel consumer options accept `onActivityChange` and optional
`activityMaxEntries`. Both the single-channel and multiplexed consumers maintain
an independent activity tracker for each opted-in channel. Named signals update
it before the raw `onSignal` callback. Presence departures remove the sender's
existing activity before `onPresence`, retaining ordering markers for those
identities. Backend signals have no presence member, so explicit clears and TTL
continue to govern them.

Disconnect, abort, resync, and unsubscribe acknowledgement handling reset activity
and ordering memory. Reconnect starts empty and uses new live signals; there is
no activity replay. Cleanup resets every channel even if an activity callback
throws. Expiry notifications remain timer-driven and activity callbacks are
synchronous; applications should handle UI exceptions inside the callback.
Capacity defaults to 1024 identities including ordering markers, configurable
from 1 to 8192 when activity tracking is enabled. Standalone trackers expose
`removeMember(channel, memberId)` for equivalent custom lifecycle integration.
No server migration or new API is required.

### Activity aggregation and presence directories

The Node/browser SDK exports `aggregateRealtimeActivity` to join a channel's
current named activity with its current presence members. The result separates
all `viewers` from matching `active` members and includes member and connection
counts. Filters support signal names and an excluded member ID. An optional
label resolver reads presentation state; labels do not confer verified identity.
Use principal-scoped presence consistently for server grouping by verified
principal. Helpers group only by server-issued member IDs, never state fields.

Activities include cloned data and presence state, supporting labeled remote
cursors without imposing a cursor payload schema. Expired activities are excluded;
feed current tracker/lifecycle snapshots to keep projections fresh. Optional
unknown senders, such as backend activity, contribute no viewer/connection count.
`formatRealtimeTypingSummary` produces English text such as “Alice and 2 others
are typing”; localization can use the participant list directly. Render labels
as plain text. A live named typing marker denotes activity; ending typing requires
a clear or expiry rather than a specific arbitrary data payload.

`createRealtimePresenceDirectory` assembles chunked snapshots and subsequent
membership events, returning isolated snapshots and emitting per-channel changes.
Incomplete snapshots do not replace visible presence. Reset it on reconnect,
unsubscribe, and teardown; it is independently owned from lifecycle activity
trackers. Capacity defaults to 1024 stored members, including staging, configurable
from 1 to 8192. Capacity failures preserve previous state. Aggregation helpers are
local UI projections and require no migration or new server API.

### Private activity scopes

Use `realtimeActivityScopeChannel(parentChannel, scope)` to create a canonical
subscription channel for a document section, breakout room, or conversation.
Scope channels are independent subscriptions: signals, presence, coalescing,
activity trackers, and summaries use that channel identity throughout. Parent
channel subscribers and other scopes do not receive these frames. Existing
endpoint ownership and subscription limits apply; each scope consumes one of the
eight channel slots per connection. Ordinary message/history APIs can still use
these channels; the namespace provides isolation rather than a separate transport.

Before admitting a scoped subscription, Gregale calls the existing
`/realtime/authorize-channel` backend callback with `permission: "read_activity"`,
canonical `channel`, `activity_parent_channel`, and `activity_scope`, alongside
the verified principal and existing connection identity. Grant only when that
principal belongs to the requested audience. There is no fallback to parent
permission; callback failure or rejection denies the subscription. Existing
callbacks must explicitly implement this permission and must not blindly grant
all channel requests. Membership changes follow existing subscription semantics:
revoke/disconnect a live connection to immediately remove access; authorization
runs again when it reconnects. No new membership database is created.

Canonical channels are `__activity.` followed by base64url-encoded UTF-8 parent,
a dot, and base64url-encoded scope, without padding. The prefix is reserved;
malformed, noncanonical, or nested scope channels are rejected. Parent and scope
omit surrounding whitespace, NUL, slash, question mark, hash, CR, and LF. Scope
labels are at most 64 UTF-8 bytes, and the complete derived channel must fit the
existing 256-byte channel limit. Go and SDK helpers implement the same encoding.
Choose shorter parent names if encoding would exceed that bound.

Backends can POST
`/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/activity-scopes/{activity_scope}/signals`
with the existing signal body. App ownership, MFA, deploy-write scopes, payload
limits, expiry, and per-derived-channel signal rate limits still apply. This API
uses backend publish authorization; client subscription membership is checked by
the application callback. Client sends use existing scoped channel actions.
Activity clears through existing lifecycle handling when the scope subscription
ends; independently owned presence directories must also be reset. No migration
is needed.
