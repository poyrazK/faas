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
matching connection through the management API, or enable the
[principal access controls preview](#principal-access-controls-preview).

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
`20261008180000201_managed_realtime_inbox_webhooks.sql` before upgrading apid.
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
Apply `20261008190000544_managed_realtime_notification_fallbacks.sql` before
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
version 1. Apply `20261008200000549_managed_realtime_message_mutations.sql`
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
Apply `20261008210000379_managed_realtime_read_progress.sql`, then upgrade apid,
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
persist for the endpoint's lifetime, with a cap of 1115 principal/stream
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

Each endpoint supports 16 devices per principal and 1115 devices total. The ledger
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

Apply migration `20261008220000559_managed_realtime_push.sql` before starting an
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

Apply `20261008230000833_managed_realtime_push_preferences.sql` after the push
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

Apply `20261009000000328_managed_realtime_push_digests.sql` after the preferences
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

Apply `20261009010000634_managed_realtime_notification_priority.sql` after the
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

The migration `20261009020000050_managed_realtime_notification_ttl.sql` follows
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

Apply `20261009030000163_managed_realtime_notification_collapse.sql` after the
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

Apply `20261009040000359_managed_realtime_push_rate_limits.sql` after the collapse
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

Apply `20261009050000366_managed_realtime_scheduled_notifications.sql` after the
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

Apply `20261009060000888_managed_realtime_notification_timeline.sql` after the
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

Apply `20261009070000012_managed_realtime_notification_webhooks.sql` after the
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

Apply `20261009080000572_managed_realtime_channel_snapshots.sql` after the
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
per endpoint; new batches return 429 at capacity. Existing 1115-message/24-hour
channel retention still applies. Batch-created message IDs use a reserved derived
`batch:<SHA256(batch_id)>:<zero-based-index>` convention; avoid these IDs for
independent single publishes.

The Go client exposes `PublishManagedRealtimeChannelBatch`. Node/browser exports
include `publishRealtimeChannelBatch`, taking a stable ID, byte payloads and a
callback that submits its request through your authorized backend transport.
Do not expose management credentials to browsers.

Apply `20261009090000912_managed_realtime_atomic_batches.sql` after the snapshots
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

Apply `20261009110000048_managed_realtime_event_schemas.sql` after the subscription
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

Apply `20261009120000338_managed_realtime_channel_reducers.sql` after the event-schema
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

Capacity defaults to 1115 identities including ordering markers; configure
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
Capacity defaults to 1115 identities including ordering markers, configurable
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
trackers. Capacity defaults to 1115 stored members, including staging, configurable
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
revoke/disconnect a live connection to immediately remove access, or use the
[principal access controls preview](#principal-access-controls-preview) for a
durable deny across nodes. Application authorization runs again when it reconnects.

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
## Transactional publishing preview

Use a customer PostgreSQL outbox when a business write and its realtime event
must commit together. See [ADR-1112](../adr/1112-transactional-realtime-publishing.md)
and [Gregale Commit setup](../gregale-commit.md) for the existing source registration,
database binding, credentials and network qualification.

1. Apply `20261012102700452_realtime_commit_receipts.sql` on the control plane.
2. Install `pkg/commit/realtime_schema.sql` explicitly in the customer database,
   alongside the existing Commit schema and source binding. Grant the managed
   relay role SELECT, UPDATE and DELETE on `public.gregale_realtime_outbox`; grant
   the application writer INSERT. The relay never installs or repairs tables.
3. Set `FAAS_COMMIT_RELAY_ENABLED=true` and
   `FAAS_REALTIME_TRANSACTIONAL_PREVIEW_ENABLED=1` on schedd. The separate
   realtime resume preview must be enabled for v2 subscribers. Existing apid
   retained-history preview gates still control management reads.
4. Write your business change and enqueue its event with the same transaction:

See the [customer transaction example](../realtime-customer-examples.md#transactional-publication) for business writes and outbox insertion on one connection.

The managed dispatcher scans at the existing five-second source cadence and
claims up to 32 realtime events per source tick. It publishes UTF-8 text frames
of at most 4096 bytes to retained history, preserving schema/reducer validation
and endpoint message limits. The target endpoint must be enabled and belong to
the source's account and app. V2 subscribers recover through existing polling;
publication is not proof of client receipt. Raw live-only subscribers are not
targets of this preview.

Delivery inspection reads metadata directly from the application database,
without returning payloads. `published` means the platform committed the channel
message and its durable receipt. Sequences are decimal strings in the Node SDK.
The preview aggregates this queue into Commit source backlog, but the existing
Commit blocked-event and replay APIs address invocation events only. Use the
realtime database helpers to inspect and retry realtime rows. Retry does nothing
for missing, published, unblocked or actively leased rows.

Transient failures retry with a five-second delay; `invalid_event`,
`destination_unqualified`, and `identity_conflict` block for repair. Pending and
blocked rows are retained until handled; published rows are cleaned after the
configured Commit retention window (seven days by default). Platform receipts
survive channel expiry and customer cleanup until source deletion. Never edit an
accepted identity's endpoint, channel, data or metadata; publish a new event ID.
Different events can be dispatched out of business-commit order. This preview
does not provide CDC or exactly-once client processing.
## Principal access controls (preview)

Managed access controls remove the need to enumerate sockets when a user leaves
a workspace or logs out across devices. The endpoint must use `oidc_jwt`.
Apply `20261012102700456_realtime_principal_access.sql`, then enable
`FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1` on every realtimed node **before** enabling
the flag on apid. Each node needs the existing private history/fleet connection,
including nodes serving live-only OIDC clients. Keep enforcement enabled while
stored revocations must remain effective. See [ADR-1113](../adr/1113-managed-realtime-access-revocation.md).

Use the account-authenticated, app-scoped API (MFA and the normal read/write
scopes are required):

```typescript
import { revokeRealtimeChannelAccess, restoreRealtimeChannelAccess,
  revokeRealtimeSessions, RealtimeService } from '@gregale/sdk-node';

const user = { slug: 'workspace', id: endpointId, principal: 'user-123' };
await revokeRealtimeChannelAccess(user, 'project-42');
await revokeRealtimeSessions(user);
const controls = await RealtimeService.getManagedRealtimeAccess(user);

// After membership is restored, remove the deny. Your authorize-channel
// callback must still grant access, and the client must subscribe again.
await restoreRealtimeChannelAccess(user, 'project-42');
```

The equivalent HTTP surface is GET/POST
`/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access`.
POST accepts `action: revoke_channel | restore_channel | revoke_sessions`.
Channel actions require `channel`; session revocation must omit it. A response
contains `denied_channels`, an optional `sessions_revoked_before`, `revision`,
and `updated_at`. Revision zero means no stored controls.

Revoking a channel removes matching subscriptions on nodes after they refresh
access state, and prevents new subscriptions. The deny also covers that
channel's private activity scopes. Queued frames from the denied channel are
dropped; frames already sent cannot be recalled. Other channels and direct
inbox messages remain available. V2 clients receive an `error` frame with
`code: access_revoked`. The current SDK channel consumer treats this as a
terminal subscription error; restart the consumer after restoring membership.

Revoking sessions disconnects OIDC sockets whose signed token `iat` is at or
before the server cutoff, and rejects those tokens on reconnect. With this
preview enabled, OIDC tokens need a positive integer `iat` no later than the
node clock. A replacement token needs `iat` strictly **after** the cutoff;
minting it in the same second may still be rejected. Synchronize issuer and node
clocks. A fresh token can reconnect, so permanent bans remain an application
authentication decision. Restoring a channel never clears the session cutoff.

Nodes refresh per connection every second with a two-second RPC timeout.
Admission and delivery fail closed when controls cannot be read; a recovered
node reads durable state before resuming delivery. API success confirms a
database commit, not instantaneous fleet convergence. Qualification must account
for one access read per active OIDC connection per second.

Preview guards allow 1115 principal records per endpoint and 64 denied channels
per principal. Session cutoffs and principal records remain until endpoint
deletion; they are not expired automatically. Static bearer and unauthenticated
endpoints do not support per-user revocation.

### Retained WebSocket channel authorization refresh

Retained v2 WebSocket subscriptions reuse SSE's 30-second application channel
grant lifetime ([ADR-1385](../adr/1385-retained-channel-authorization-refresh.md)).
The history pump checks idle subscriptions, and the writer rechecks expired
grants before sending queued channel messages, presence, signals, read receipts
or checkpoints. Channel commands also refresh an expired grant before executing.
Concurrent checks of one subscription share a callback. Changes
to endpoint policy invalidate its cached grant and use the current callback
address and token. No positive grant is extended while a refresh is unavailable.

HTTP 401/403 from the authorization callback remove that subscription and emit
`access_revoked`, including its presence and route cleanup. Other non-2xx statuses,
transport errors and timeouts close the connection with WebSocket code 1013 so
clients may reconnect; they do not assert permanent revocation. Idle cleanup
includes the configured history poll interval and callback deadline. Already
written frames cannot be recalled. These callbacks may wake the application and
keep it billable while subscriptions remain active.

Retained JWT connections also cache verification of their pinned subject and
session for up to 30 seconds ([ADR-1332](../adr/1332-session-bound-retained-realtime.md)). An
expiry timer closes idle sockets, and all output checks token validity. Expired
or changed identities close with code 1008; verification outages use 1013.
Custom principal projections must still match. Tokens require `exp` in the
production retained transports; clients reconnect with a refreshed token.
Signing-key removal follows the existing JWKS cache freshness/grace contract.

The Auth template now requires a verified `session_id` in channel callbacks and
uses `gregale_auth.authorize` in a transaction scoped to that session. Upgrade
the daemon, redeploy Auth apps from the new template and reconnect clients. An
older Auth app still uses subject-wide checks; an updated app fails closed on
callbacks from daemons that omit the session ID. Legacy management and
inbox/direct-message session authorization retain their existing contracts.
Ticket subscriptions continue using their existing one-second durable checks.
Before deployment qualification, exercise membership removal, bans/global
sign-out, callback outages, policy rotation and slow consumers across nodes.
Existing preview gates and production qualification requirements still apply.

### PostgreSQL table-change feeds (operator preview)

A table-change feed queues selected row changes automatically into the transactional
realtime outbox. Enable and qualify the transactional realtime source described
above first. The same relay, receipts, retry inspection, endpoint ownership and
retention rules apply. This is explicit trigger capture, not WAL CDC or a backfill.

As the customer database owner, install `pkg/commit/realtime_changefeeds.sql`
**after** the Commit binding and realtime outbox schemas. Go callers can obtain
that SQL from `commit.RealtimeChangefeedSchema`. The platform never installs it
for you. Use a regular table with a primary key; partitioned tables are unsupported.

```ts
import { installRealtimeChangefeed, removeRealtimeChangefeed } from '@gregale/sdk-node';

// Run using the customer database owner connection, inside its own transaction.
await installRealtimeChangefeed(ownerTx, 'public.orders', {
  name: 'order_status',
  endpointId,
  channel: 'orders:', // followed by tenant_id, e.g. orders:tenant-42
  tenantColumn: 'tenant_id',
  columns: ['id', 'tenant_id', 'status'],
});
// To stop future capture (existing events remain queued):
await removeRealtimeChangefeed(ownerTx, 'public.orders', 'order_status');
```

Omit `tenantColumn` for a static channel. Allowlist every primary key and tenant
field explicitly. Do not include secrets. Channel names must satisfy realtime's
existing validation; tenant values must be nonempty and contain no whitespace,
control characters, `/`, `?` or `#`. Application authorization must independently
verify each subscriber's tenant membership. Feeds do not grant channel access.

Each event is a JSON text frame with this shape:

```json
{"type":"database.change","feed":"order_status","operation":"update","key":{"id":42},"before":{"id":42,"tenant_id":"tenant-42","status":"pending"},"after":{"id":42,"tenant_id":"tenant-42","status":"shipped"}}
```

Inserts have `before: null`; deletes have `after: null`. Updates to fields outside
the allowlist, or unchanged selected values, do not emit. Tenant changes are
rejected: use an explicit delete/insert in one transaction to notify both channels.
A changed primary key appears in the before and after images. PostgreSQL JSON
numbers may exceed JavaScript integer precision; use text keys where required.
Subscribe using existing retained v2 channel SDKs. Raw live-only subscribers do not
receive outbox publication. Multiple feeds produce separate events without a
cross-channel ordering guarantee.

The business writer requires SELECT/INSERT privileges needed by its operation and
INSERT access to `public.gregale_realtime_outbox`; functions run as the writer,
never with owner privileges. Invalid routing, missing privileges, or an event
exceeding the existing 4096-byte frame bound abort the business write. That bound
includes the envelope and both selected row images. Monitor outbox backlog and
blocked deliveries using the existing delivery helpers.

Remove and reinstall a binding inside one owner transaction to change its config.
Review bindings before table/column migrations; dropping or renaming configured
columns can cause writes to fail. TRUNCATE produces no row events. Existing rows
are not emitted on installation. Owners must not disable triggers or use a
replication write mode that bypasses them. No platform migrations or production
activation are part of installing these source files.

### HTTP SSE channel subscriptions (operator preview)

Receive retained channel events over HTTP using the same managed endpoint URL:

```text
GET /__gregale/realtime/{endpoint_id}?transport=sse&channel=orders%3Atenant-42
Authorization: Bearer <OIDC JWT>
Accept: text/event-stream
Last-Event-ID: 123
```

Requires the existing realtime resume preview and private history client. Managed
access preview, when enabled, enforces channel denies and session cutoffs too.
Without a subscription ticket, static bearer and unauthenticated endpoints are unsupported. Browser requests
require an explicit matching `AllowedOrigins` policy; OPTIONS preflight permits
Authorization and Last-Event-ID. Tokens are never accepted in query parameters.

The initial cursor defaults to zero; `after` overrides Last-Event-ID. SSE IDs and
message `sequence` values are decimal strings, avoiding integer precision loss.
`message` frames contain channel, sequence, data_base64, binary, metadata,
target_message_id, version, message_event and deleted. `subscribed` and heartbeat
frames contain no cursor. Expired history, ahead-of-head cursors and sequence gaps
produce `resync_required` and close the stream. Refresh your application snapshot
and explicitly choose the subsequent cursor; do not automatically skip history.

```ts
import { subscribeRealtimeSSE } from '@gregale/sdk-node/browser';

for await (const message of subscribeRealtimeSSE({
  url: `https://your-gateway/__gregale/realtime/${endpointId}`,
  channel: 'orders:tenant-42',
  token: () => getFreshOIDCToken(),
  after: savedSequence,
  signal: abortController.signal,
})) {
  await applyEvent(message); // decode data_base64 according to binary
  savedSequence = message.sequence;
}
```

The fetch-based helper reconnects network failures, HTTP 429 and server errors
with jittered exponential backoff, capped at ten seconds. It obtains a new token
on each attempt. Authentication, protocol, access and resync errors stop iteration
with `RealtimeSSEError`. Streams rotate at most every five minutes. Delivery may
repeat; apply events idempotently. Cancellation uses AbortSignal.

SSE is receive-only, with one retained channel per HTTP connection. It shares
connection and node subscription limits with WebSockets, and retains one bounded
history page rather than a growing output queue. Slow writes hit the existing
write deadline and disconnect. Identity/channel authorization is rechecked at most
every 30 seconds and durable access controls before each event (snapshot at most one
second old). A definitive deny sends `access_revoked`; transient lookup, callback or
history failures close the stream without an error event so clients reconnect.
SSE connections are not represented in the WebSocket connection directory,
presence or connect/disconnect callbacks. No ACK, durable server checkpoint,
inbox, signal or client publish operation is provided by this transport.

The managed gateway proxy supports HTTP streaming. Before production activation,
qualify proxy flushing, idle timeouts, cancellation, reconnect/replay, revocation
and slow-client behavior across the deployed route. Runtime tests and fleet
qualification have not been executed for this preview implementation.

### Retained channel-event webhooks

Forward retained channel events to existing signed app webhooks. Apply
`20261012102700459_realtime_channel_webhooks.sql` before upgrading apid or accepting
these bindings, and run the existing webhook event-outbox relay and dispatcher.
No customer database installation is required for this platform feature.

Create or update an app webhook to explicitly include `realtime.channel.published`
in its event_filter. Then configure its exact route:

```http
PUT /v1/apps/demo/webhooks/{webhook_id}/realtime-channel
Authorization: Bearer <platform token>
Content-Type: application/json

{"endpoint_id":"0123456789abcdef0123456789abcdef","channel":"orders:tenant-42","metadata":{"kind":"order"}}
```

The endpoint must belong to the same app/account. Omit metadata or supply `{}` to
accept all events on that channel; otherwise every supplied key/value must match.
One route per webhook; use separate webhooks for additional exact channels within
existing webhook quotas. No glob/prefix channel matching. Empty catch-all webhook
event_filter does **not** opt into channel payload delivery.

GET on the same route reads the binding; DELETE stops future capture and returns
204. PUT replaces the route and metadata filter. GET of an unbound hook returns
404. Changing bindings/event filters does not rewrite already queued recipient
lists or payloads. These operations use existing account/app authorization, MFA,
read/write scopes and webhook plan eligibility.

The webhook payload contains event_id, app_id, endpoint_id, channel, sequence
(decimal string), data_base64, binary, metadata, target_message_id, version,
message_event, deleted and occurred_at. The existing signed webhook envelope and
receiver-verification contract apply. Review the allowlisted data and destination:
this event explicitly forwards retained message payloads to an external backend.

Capture and retained acceptance are atomic. It includes scheduled publications,
transactional outbox/table feeds, batch items and channel message edits/deletes.
Each new retained sequence gets an event; rejected or rolled-back writes and
idempotent retries of an existing accepted message get none. Empty recipient sets
produce no webhook outbox row. Existing history is not backfilled. Live-only
publishes, ephemeral activity, signals and inbox messages use their existing
separate contracts and do not emit this event.

Use the existing webhook deliveries/attempts/retry API or CLI for inspection and
manual retry. Delivery is at least once and may be out of order; deduplicate using
payload event_id. Decouple receiver processing from HTTP acknowledgement. Avoid
republishing a webhook back into its matched channel without a loop guard. Later
redaction or history expiry cannot recall captured webhook payloads. Database
migration, runtime acceptance and production activation remain unexecuted.

### Short-lived subscription tickets (operator preview)

Your backend can authorize a user and issue a short-lived realtime credential
without configuring an OIDC provider. Apply
`20261012102700462_realtime_subscription_tickets.sql` and the preceding principal
access migration. Set `FAAS_REALTIME_TICKETS_PREVIEW_ENABLED=1` on apid and all
realtimed nodes, with resume preview and the private history RPC configured.
Keep these gates off until operator acceptance. No migrations or activation were
executed during implementation.

Issue a ticket using your backend's app-scoped platform credential:

```http
POST /v1/apps/demo/realtime/endpoints/{endpoint_id}/tickets
Authorization: Bearer <backend platform token>
Content-Type: application/json

{"principal":"customer-42","channels":["orders:tenant-42"],"ttl_seconds":120}
```

The API requires normal app ownership, realtime plan eligibility, MFA and write
scopes. Returns 201 with `{ "ticket": "grt_...", "expires_at": "..." }` and
Cache-Control: no-store. TTL is 1-300 seconds, with 1-32 unique exact channels.
Endpoint must be enabled. At most 1115 unexpired tickets per endpoint; expired
records are pruned on subsequent issuance and endpoint deletion removes all.
These are operator-preview bounds, not a new pricing tier.

**The backend is the authorization authority.** Authenticate the caller with your
own login system, derive the principal from that session, and check each requested
channel before issuance. Never put a platform credential in the browser or forward
an unchecked user-supplied principal/channel list. The ticket replaces application
connect/channel callbacks for its subscription path. It does not change existing
endpoint authentication for other clients: a static-bearer endpoint with a
backend-held secret can keep the legacy path restricted without an OIDC provider.

Browser WebSocket example:

```ts
import { createBrowserRealtimeTicketSocketFactory, consumeRealtimeChannel }
  from '@gregale/sdk-node/browser';

const createSocket = createBrowserRealtimeTicketSocketFactory(async () => {
  const response = await fetch('/api/realtime-ticket', { method: 'POST' });
  if (!response.ok) throw new Error('Could not obtain realtime ticket');
  return (await response.json()).ticket; // your authenticated backend endpoint
});
// Supply webSocketFactory: createSocket to consumeRealtimeChannel's options.
```

The factory obtains a fresh ticket on every reconnect. Use the existing v2 channel
consumer and client-held cursor. Browser handshakes still require explicit matching
AllowedOrigins. Ticket connections allow subscribe, unsubscribe and non-durable
ack only; exclude durable subscription names, read receipts, direct messages,
inboxes, signals and presence state. They do not create presence leases, send
connect/disconnect callbacks or receive ephemeral channel fanout. New retained
messages arrive through the existing history poll interval.

For SSE, pass an authenticated backend ticket provider as `subscribeRealtimeSSE`'s
`token` callback. Tickets travel in Authorization headers (or the existing browser
WebSocket bearer subprotocol), never query strings. SSE terminates at expiry and
reconnects with a new ticket. WebSocket tickets expire and close active sockets;
the normal channel consumer reconnects using the factory. Renewal changes neither
history retention nor resync requirements. A ticket may be reused before expiry;
it is not a single-use nonce.

Set `FAAS_REALTIME_ACCESS_PREVIEW_ENABLED=1` on apid to use the existing principal
access APIs for ticket endpoints, including non-OIDC endpoints. Session revocation
rejects tickets issued at/before the cutoff; channel revocation removes that
channel from ticket permissions. Fresh tickets still respect channel denies.
Granting a parent channel does not grant its private activity scopes; explicit
child grants are required, and a parent deny also blocks child scopes. Nodes
recheck tickets at most once per second; verification failure closes the stream.
Revocation cannot recall an in-flight frame. Restoring channel access requires
resubscription. Individual ticket revocation is not provided; use principal session
revocation or wait for expiry.

Store tickets only in client memory. Issuance returns the credential once; platform
storage contains its hash, and audit events omit credentials and principals. The
private apid lookup must be available throughout a session; failure is closed.
Qualify expiry/renewal, scope denial, revocation, concurrency admission, slow readers,
browser origins and private RPC failures before production activation. Runtime
tests remain deferred.

### Automatic receive-only transport fallback

The SDK exports `consumeRealtimeChannelWithFallback` from its main and browser
entries. It starts with WebSocket and switches to SSE after repeated connection
or subscription failures, preserving the last fully processed client cursor.
Requires the existing server resume/SSE preview and matching browser allowed
origins; ticket authentication also requires ticket preview.

```ts
import { consumeRealtimeChannelWithFallback } from '@gregale/sdk-node/browser';

await consumeRealtimeChannelWithFallback({
  url: `https://your-gateway/__gregale/realtime/${endpointId}`,
  channel: 'orders:tenant-42',
  token: getFreshRealtimeCredential, // OIDC JWT or backend-issued ticket
  cursorStore, // existing numeric load()/save(sequence) contract
  onMessage: async (message) => {
    await applyEvent(message); // data is Uint8Array on both transports
  },
  onTransportChange: ({ transport, reason }) => {
    console.log('Realtime transport selected', transport, reason);
  },
  signal: abortController.signal,
});
```

HTTP(S) and WS(S) endpoint URLs are accepted and converted to matching schemes
without changing the host/path. Node callers provide an authenticated
`webSocketFactory`; the browser default obtains a fresh token or ticket on each
attempt. SSE also refreshes credentials for every reconnect. Do not use URL query
parameters to carry credentials.

The defaults are two failed WebSocket attempts and a five-second connection/
subscription deadline. `fallbackAfter` accepts 1-10; `connectTimeoutMs` accepts
100-60000. A subscription healthy for thirty seconds resets the failure streak.
Normal ticket-expiry closure renews the WebSocket credential without counting as
a transport failure. SSE selection is sticky until a new helper invocation.
`onTransportChange` reports selection, not successful connection or health.

Only one transport consumes at a time. The helper waits for active message/cursor
callbacks before handing off. Message processing completes before cursor save;
failures in either callback terminate without falling back. Both transports yield
channel, sequence, data, binary, metadata, targetMessageId, version, event and
deleted. WebSocket-specific messageId is omitted; deduplicate by channel/sequence.
The shared numeric cursor contract requires safe JavaScript integers. Use direct
SSE with decimal strings if sequences exceed that range.

Expired history raises `RealtimeResyncRequiredError` on either transport. Optional
`onResync` can rebuild your application snapshot and return its represented
sequence; that cursor is persisted before reconnecting. The helper does not
silently reset or skip retained history. Presence, ephemeral signals, read progress,
inboxes, direct messages, metadata filters and named durable subscriptions are
unsupported by this receive-only API; use their existing transport-specific APIs.

Explicit WebSocket policy/protocol rejection, protocol error frames, credential
provider failures and SSE 401/403 are terminal. Browsers hide failed handshake
status, so an ambiguous failure may lead to an SSE attempt; SSE independently
authenticates and will not bypass denial. Tokens and tickets retain identical
channel permissions across both transports. Cancellation closes active streams;
application callbacks must implement their own cancellation if necessary.
Runtime transport/failure tests remain deferred.

### Dashboard realtime event inspector

Open `/dashboard/apps/{slug}/realtime` from the app’s “Inspect realtime events”
link. The page uses existing dashboard sessions, app permissions, realtime plan
eligibility and history preview. Responses are no-store, payloads are HTML-escaped,
and event data is not placed in query parameters or audit logs.

Choose an endpoint, channel and after-sequence to browse up to 50 retained events
per page. Each event shows sequence, UTC timestamp, mutation version/event and
publisher message ID, with expandable metadata and payload. JSON is formatted;
binary payloads are base64. Oldest/latest bounds and next-page controls are shown.
An expired or ahead-of-head cursor displays a resync notice. “Browse from the oldest
retained event” is an explicit inspection action, not a client checkpoint reset.

For transactional publishing, apply
`20261012102700465_realtime_inspector_observations.sql` before running this version
of schedd with transactional preview enabled. The relay copies no payloads or
credentials: only a sample of event IDs, endpoint/channel, attempt count, blocked
code, creation time and observation time. Up to 32 unpublished rows per source are
mirrored, prioritizing blocked events. Source health counters cover both ordinary
Commit and realtime events. Choose a source to filter its sample to this channel.

Observations can be stale or incomplete, especially while the source is offline,
paused or unqualified. An empty sample is not proof of publication. Use the
customer database delivery helpers for authoritative per-event status and for
repair/retry of blocked rows. The inspector does not connect to customer databases,
expose sealed source credentials or add an outbox mutation operation.

Choose a webhook to see matching channel events among its latest 20 deliveries.
Open a delivery to inspect attempts; use existing webhook page controls to retry
eligible dead deliveries with normal CSRF, MFA and app authorization. Older
webhook deliveries remain available on the full webhook page. Channel and source
listings are bounded, and no automatic polling runs while this page is open.

Source observation storage requires the PostgreSQL managed Commit implementation;
in-memory deployments can inspect retained history only. Database migration,
runtime UI verification and production activation remain unexecuted in this session.

### Backend-visible processing acknowledgement status

For an authenticated WebSocket consumer using `durableSubscription`, the existing
Node SDK acknowledges each message after its handler resolves. Your backend can
inspect the latest actual advancing ACK:

```http
GET /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/acknowledgement?principal={principal}&subscription={name}
Authorization: Bearer <account-api-key>
```

The response contains decimal-string `checkpoint_sequence`, nullable
`acknowledged_sequence` and `acknowledged_at`, and `updated_at`. The endpoint is
read-only and requires the history preview, the endpoint's app permissions, MFA
and read scope. Missing/expired checkpoints return 404. Queries do not renew the
30-day inactivity retention. A new baseline has null ACK fields; reset clears
them. Existing checkpoints receive no historical ACK backfill.

To receive receipts, create or update an enabled **app-scoped webhook** with an
explicit `event_filter` containing `realtime.channel.acknowledged`. No channel
binding is needed. This opts into receipts across the app's endpoints/channels.
Each advancing ACK atomically queues `event_id`, `app_id`, `endpoint_id`,
`principal`, `subscription`, `channel`, decimal-string `sequence` and `occurred_at`.
Message payloads are excluded. Duplicate/stale ACKs, initial checkpoints, load
refreshes and resets do not queue receipts. Existing signature verification,
retry policy, delivery history and manual retry controls apply. Deduplicate on
`event_id`; deliveries may arrive out of order.

Use the ACK sequence as a consumer assertion about that delivery. Status preserves
only the latest advancing ACK: it is not an individual-message receipt ledger and
must not imply that all preceding messages were processed (filters or recovery
may have skipped them). A custom client can acknowledge without performing work.
Use distinct subscription names for independent devices. Human read progress
remains separate; there is no exactly-once side-effect guarantee. SSE and
subscription-ticket connections do not participate in named durable ACKs.

Apply `20261012102700468_realtime_channel_acknowledgements.sql` before deploying the
updated cursor writer. Migration execution and runtime checks are still deferred.

### Shared backend-worker consumer groups

Consumer groups let several backend workers compete for the same channel's work.
Each claim has one current lease, and expired claims can be reassigned. This is an
account-authenticated HTTP pull API under the existing history preview, with
realtime plan/app permissions and MFA. Reads require read scope; worker operations
require write scope. The preview requires the durable PostgreSQL store. Use a
backend account API key; do not expose it to browsers or mobile clients.

Create a group with an explicit assignment baseline:

```http
POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups
Authorization: Bearer <backend-account-api-key>
Content-Type: application/json

{"name":"email-workers","after_sequence":"0","lease_seconds":30,"max_attempts":5}
```

`after_sequence` is a decimal string and must not exceed the latest sequence.
Zero starts at the beginning if still retained; a cursor below the retention
floor creates a group requiring explicit recovery. Creation is exclusive: using
an existing name returns 409. Configuration is immutable; deleting a group also
removes all its outstanding payloads and invalidates its claims.

Under `/groups/{group}`:

| Method and suffix | Behavior |
| --- | --- |
| `GET` | Assignment cursor, unassigned lag, completion count, ready/delayed work, live/expired leases, dead letters, active worker names and retention/capacity indicators |
| `POST /claims` | Body `{"worker":"worker-a"}`; return one message, attempt, opaque `claim_token` and `lease_until` |
| `POST /claims/settle` | Sequence/token and action `complete`, `retry` or `renew` |
| `GET /dead-letters` | Bounded original message snapshots, attempts and diagnostic codes |
| `POST /dead-letters/{sequence}/replay` | Queue a dead letter again with a fresh attempt budget |
| `POST /resync` | Explicit recovery with expected cursor and the retained floor minus one |
| `DELETE` | Remove group, claims and dead-letter payloads |

The collection `GET /groups` lists all groups for the channel. The realtime
inspector shows group lag, lease counts, active workers and dead letters; choose
a consumer group to inspect its failures. Active workers means distinct names on
currently unexpired leases, rather than socket/process presence.

A claim snapshots the original retained frame, including metadata and mutation
information. That snapshot survives channel expiry until completion or deletion.
Claims prefer eligible retries, then assign the next channel sequence. There is
no payload filter in the first version. Sequences and counts that can grow beyond
JavaScript precision are decimal strings. Workers may complete out of order.

Completion example:

```json
{"sequence":"42","claim_token":"<opaque-token>","action":"complete"}
```

For long work, send `renew` before the lease expires. Renewing keeps the same token
and does not consume another attempt. To retry a failed handler:

```json
{"sequence":"42","claim_token":"<opaque-token>","action":"retry","retry_after_seconds":5,"failure_code":"handler_failed"}
```

Do not put payloads, secrets or exception text in `failure_code`. It is a printable
ASCII code of at most 64 bytes. Tokens are credentials; never log them. The server
stores only their hashes and excludes claim responses from generic response
caching. Lost/expired/replaced/already-completed claims return 409 with
`realtime_group_lease_lost`; stop work and do not complete with the old token.
Duplicate completion is deliberately not an exactly-once receipt.

Default lease/attempt settings are 30 seconds and 5 attempts; bounds are 1–300
seconds and 1–10 attempts. Retry delay is 0–300 seconds. Admission allows 8 groups
per endpoint, 32 live leases and 128 outstanding snapshots per group. Dead letters
count against that snapshot cap. Claims return 204 when caught up, delayed work
is not ready, or all lease slots are busy, and 429 if the outstanding cap prevents
new assignment. Completion frees capacity. Replay or delete unused groups to
resolve retained failure backlogs. Polling drives expiry normalization; exhausted
expired leases are also visible in the dead-letter listing before the next claim.

A group below retained history returns 409 `realtime_group_resync_required` when
trying to assign new work, while existing snapshot retries can still be claimed.
First finish pending work and recover missing effects from your application state.
Read `assigned_sequence` and `oldest_sequence`, then send:

```json
{"expected_sequence":"<current-assigned-sequence>","after_sequence":"<oldest-sequence-minus-one>"}
```

Recovery is permitted only with no ready/leased snapshots and a matching expected
cursor, advancing exactly to the retained floor minus one. Dead letters and the
completion count survive. A reset does not assert skipped work was completed.

The Node worker helper renews leases while a handler runs and ACKs after success:

```ts
import { consumeRealtimeConsumerGroup, RealtimeService } from '@gregale/sdk-node';

const target = { slug: 'my-app', id: endpointId, channel: 'tasks', group: 'email-workers' };
await consumeRealtimeConsumerGroup({
  ...target,
  worker: 'worker-a',
  signal: shutdown.signal,
  async onMessage(message, { signal }) {
    await processIdempotently(message.sequence, message.data_base64, signal);
  },
});
```

Configure the SDK's backend account credentials as usual. Handler errors request
retry with `handler_failed`; optional `onHandlerError` observes them without
sending error text to the server. Network/auth/lease errors terminate the helper;
restart it with your normal process backoff. Shutdown aborts the handler signal,
stops renewal and leaves the outstanding lease to expire. The handler must honor
that signal. Each helper processes one message at a time; run several workers to
increase concurrency. Node and Python generated clients expose individual claim,
settlement, retry/replay and recovery methods.
Python workers can use `consume_realtime_consumer_group` or its scoped-credential
variant for the managed async lifecycle. Low-level callers should use the generated
`sync_detailed` / `asyncio_detailed` methods and inspect `status_code`: a null parsed body alone cannot distinguish a
204 empty claim from a documented error response.

Use idempotent effects keyed by endpoint/channel/sequence: a crash after effects
and before completion, or a handler ignoring lease loss, can duplicate processing.
Shared group completion is separate from per-principal durable ACKs and human read
receipts; it does not emit `realtime.channel.acknowledged`. This is not an exactly-once
queue or an indefinite archive for work that was never claimed.

Apply `20261012102700470_realtime_consumer_groups.sql` before deploying this apid
version. Runtime concurrency, database migration and UI checks remain deferred.

### Scoped consumer-group worker credentials

Backends can now give each worker a credential for **one existing group** rather
than an account API key. The credential's group UUID also fixes its endpoint and
channel. It cannot publish events, create groups, administer credentials or call
account APIs. Deleting/recreating a group with the same name does not restore old
access. These APIs require the PostgreSQL store and the existing history preview.

An account admin issues a credential under the group's existing app path:

```http
POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/credentials
Authorization: Bearer <account-admin-api-key>
Content-Type: application/json

{"name":"worker-a","permissions":["status","claim","renew","complete","retry"],"ttl_seconds":86400}
```

The response includes one-time plaintext `credential` (`grw_…`) and `info` with
its ID, immutable `group_id`, permission list, generation and expiry. Name becomes
the worker identity displayed for its claims; use distinct names to track workers
independently. Store the credential in your backend secret manager. Never log it
or the claim tokens, put it in URLs or distribute it to browsers/mobile clients.

Under `/groups/{group}/credentials`:

| Method and suffix | Authorization and result |
| --- | --- |
| `GET` | App read scope; metadata only, no secret or hash |
| `POST` | Account admin scope and MFA; issue one group-bound credential |
| `POST /{credential_id}/rotate` | Admin scope and MFA; body `{"ttl_seconds":86400}` (or `{}` for default), return a new one-time secret |
| `POST /{credential_id}/revoke` | Admin scope and MFA; idempotently revoke access, no body |

Rotation immediately invalidates the old secret when committed, preserves
credential ID/permissions and increments `generation`. A new secret can settle
that credential's existing unexpired leases. Expired unrevoked credentials can be
rotated; revoked credentials cannot. Issuance and rotation require an enabled
endpoint and the history preview. Revocation remains available when either is
disabled. It does not cancel side effects already running outside Gregale.

Workers use **only** their credential at `/v1/realtime/workers/groups/{group_id}`:

| Method and suffix | Exact required permission |
| --- | --- |
| `GET` | `status` |
| `POST /claims` | `claim`; send `{}` or a `worker` matching the issued name |
| `POST /claims/settle` | `complete`, `retry` or `renew`, according to the action |
| `GET /dead-letters` | `dead_letters` (includes original failed payloads) |
| `POST /dead-letters/{sequence}/replay` | `replay` |
| `POST /resync` | `resync` |
| `DELETE` | `delete` |

Use `Authorization: Bearer <grw_credential>`. Account keys, session cookies,
subscription tickets and URL tokens cannot authenticate this worker surface.
Worker grant permissions are explicit: normal workers should omit the four
administrative/read-failure grants `dead_letters`, `replay`, `resync` and `delete`.
Each settlement still requires an unexpired claim token; a worker can settle only
leases issued to **its own credential ID**. The original account-key APIs remain
available for group recovery and administration.

Authentication failures return 401 and count toward the existing shared per-IP
failure budget. Missing operation permissions return 403. Store failures fail
closed. The group transaction rechecks the current secret hash, permission,
revocation, expiry, enabled endpoint and live account/app state before operating.
Rotation/revocation waits for an already-authorized group transaction to finish;
once committed, subsequent operations cannot pass with the old/revoked secret.
External work can continue until it observes cancellation, so handlers must still
be idempotent. On expiry/revocation the worker cannot renew or complete; unfinished
leases recover through the usual lease-expiry retry policy.

The isolated Node client does not change global SDK credentials:

```ts
import {
  createRealtimeConsumerGroupWorkerClient,
  consumeRealtimeConsumerGroupWithCredential,
} from '@gregale/sdk-node';

const client = createRealtimeConsumerGroupWorkerClient({
  groupId: issued.info.group_id,
  credential: async () => secretManager.currentWorkerCredential(),
});
await consumeRealtimeConsumerGroupWithCredential({
  client,
  signal: shutdown.signal,
  async onMessage(message, { signal }) {
    await processIdempotently(message.sequence, message.data_base64, signal);
  },
});
```

A credential provider runs for each request, allowing rotation without changing
account-key configuration. The helper requires `status`, `claim`, `renew`,
`complete` and `retry`; other permissions are not inferred. It uses the existing
lease renewal, success ACK, bounded retry and shutdown behavior. Renewal/auth/network
failures stop the helper and abort its handler signal. Restart it using normal
process backoff. `client.status()`, `claim()`, `settle()`, `deadLetters()`,
`replay()`, `resync()` and `deleteGroup()` expose individual operations. Generated
Node/Python APIs also expose the worker and credential-administration endpoints;
configure only the worker secret when calling the worker methods. Python callers
should inspect detailed response status to distinguish an empty claim from an error.

The inspector shows credential status, generation, expiry/revocation, latest use
and operation, and a cumulative decimal-string `use_count`. Usage counts committed
worker operations, including 204 empty claims; rejected requests and rolled-back
operations do not increment it. Rotation preserves this usage history. Credential
issue/rotation/revocation and destructive worker actions emit audit IDs, never
secrets. This is operational evidence rather than a log of all attempted requests.

Admission allows 64 credential metadata records per endpoint and 128-byte names.
Default lifetime is 1 day; supported lifetime is 1 second–30 days. On issuance,
records expired/revoked more than 7 days ago are removed before admission; otherwise
inactive metadata still consumes the cap. Group deletion removes its credentials.
Cleanup does not delete message snapshots or reassign in-flight work. An endpoint
at the credential cap returns 429 with its limit and observed count.

Apply `20261012102700473_realtime_worker_credentials.sql` before deploying this
apid version, including for existing account-key group claims. Migration execution,
runtime concurrency/auth tests and UI checks remain deferred.

## Channel-scoped publisher credentials

Account administrators can issue, rotate or revoke credentials under
`/v1/apps/{slug}/realtime/endpoints/{id}/publish-credentials`. Issue responses reveal
the `grp_` secret once; metadata lists and the inspector never reveal it. Configure
exact `channels` and `event_types`, optional `publishes_per_minute`,
`max_payload_bytes` and `ttl_seconds`. Defaults are 60 accepted events per fixed
minute, 4096 bytes and one day; maxima are 600, 4096 and 30 days. The endpoint's
smaller payload limit also applies. Each endpoint permits 64 credential records.

Producers send Bearer credentials to
`POST /v1/realtime/publishers/endpoints/{endpoint_id}/channels/{channel}/publish`
with a required `Idempotency-Key` and JSON `event_type`, `data_base64`, optional
`binary` and `metadata`. `event_type` is reserved in metadata and must agree with
the explicit event label. This retained-only API preserves schema, reducer and
channel-webhook validation. History preview and an eligible enabled endpoint are
required. Exact grants do not interpret wildcards.

Reuse the same key and decoded content after an uncertain response. A matching
retry returns the original decimal-string sequence with `replayed: true`, even
if channel history has been trimmed. Changed content returns 409. Receipts last
24 hours and have a hard cap of 1115 per credential, so sustained publishing can
reach this cap before the rate limit. Capacity/rate failures return 429. Matching
retries do not consume accepted publish quota. Minute windows permit bursts across
a boundary; this is not a rolling rate limit. Beyond receipt retention, reuse is
not guaranteed to deduplicate and can conflict with an existing retained frame.

Rotation preserves grants, receipt identity and rate counters. Revocation or
expiry prevents subsequent authorized transactions; an operation already holding
the authorization lock may complete before revocation. Inspector metadata shows
accepted/replayed counts and latest use. `durable: true, partial: true` means the
event committed but live fanout was incomplete; subscribers recover from history.

Migration `20261012102700476_realtime_publisher_credentials.sql`, runtime checks
and inspector rendering remain pending. See ADR-1032 for the preview bounds.

## Consumer-group dead-letter webhooks

Select `realtime.consumer_group.dead_lettered` explicitly on an enabled app
webhook to receive signed alerts when a group message becomes dead. Existing
webhook delivery/retry controls apply. The event contains `event_id`, `app_id`,
`endpoint_id`, `group_id`, `group`, `channel`, decimal-string `sequence`, `attempts`,
`max_attempts`, bounded `failure_code` and `occurred_at`. It excludes payloads and
credentials. Deduplicate the webhook delivery ID; retries can deliver again.

Exhausted explicit retries alert at settlement. Exhausted leases alert when the
next claim detects expiry, so inactive groups do not produce immediate expiry
alerts. Listing dead letters may show an exhausted expired lease before a claim
materializes its dead transition. No existing dead letters are backfilled. A
replayed message that exhausts its new budget produces a new alert. The enqueue
and dead transition commit together; failures roll both back.

Use the inspector's group links to view failures, and each failure's history link
to inspect its retained event. Expired source history can be absent while the
consumer-group failure snapshot remains available. Repair the worker, then use
the existing authorized dead-letter replay API. Alerts do not automatically
replay work. To construct an inspector URL from a webhook, URL-encode the app slug,
channel and group name in `/dashboard/apps/{slug}/realtime?endpoint_id={endpoint_id}&channel={channel}&group={group}`.
The dashboard requires normal account authentication.

Node processing helpers accept `failureCode(error, message)`, defaulting to
`handler_failed`. Return a safe code of 1–64 printable ASCII bytes, such as
`upstream_unavailable`; do not return exception messages. A throwing/invalid
callback stops the helper and leaves the claim to expire. Direct SDK settlement
requests continue to use the existing `failure_code` field.

Migration `20261012102700479_realtime_consumer_group_dead_letter_webhooks.sql`
and runtime/UI verification remain pending. See ADR-1033.

## Batch claims and concurrent workers

Account and scoped-worker group APIs expose `POST .../claims/batch`. Supply a
required `limit` from 1 to 16; account callers also supply `worker`. Scoped-worker
identity remains fixed by the credential and requires the existing `claim`
permission. Each result is independently leased and uses the existing renew,
complete and retry operations. The group-wide 32 active leases and 128 outstanding
snapshot limits still apply across every worker and API replica.

The batch response is always 200 with `claims`, `history_unavailable` and
`capacity_blocked`. A partial batch can contain valid committed claims plus a
blocker: process those claims before recovering the retention gap or freeing
snapshot capacity. Empty claims can also mean caught up, delayed retries or all
lease slots occupied. Unexpected transaction errors roll back every new claim.
Tokens are returned once; do not automatically retry a claim with an uncertain
outcome. Unobserved leases eventually expire. Single-claim APIs retain their
existing behavior.

Node `consumeRealtimeConsumerGroup` and
`consumeRealtimeConsumerGroupWithCredential` accept `concurrency` (1–32, default
1) and `batchSize` (1–16, default min(concurrency,16)). They request only available
handler slots, renew each lease, and settle each message independently. Completion
can be out of order. Keep concurrency 1 for sequential local processing; multiple
workers can still reorder group completion. Use idempotent effects.

Partial-batch blockers stop new claims while returned work finishes, then the
helper reports an error. Fatal network/auth/lease failures stop claims and cancel
other handlers. Shutdown cancels processing and renewals and waits for handlers
to exit; unsettled messages become available after lease expiry. Honor the handler
signal, because ignored cancellation can delay shutdown and cannot undo effects.
No additional migration is required. Runtime verification remains pending; the
existing consumer-group/credential migrations must already be deployed. See ADR-1034.

## Pause and resume consumer groups

Use account `POST .../groups/{group}/pause` and `.../resume`, or scoped-worker
`POST /v1/realtime/workers/groups/{group_id}/pause` and `.../resume`. Account callers
need app ownership, admin scope and MFA. Workers need separate explicit `pause`
or `resume` permissions; existing credentials receive neither automatically.
Both operations return `paused` and nullable `paused_at`. Repeated pause preserves
the timestamp; repeated resume is safe.

After pause commits, new single claims return 204 and batch claims return an empty
200 result with `paused: true`. No new assignment or lease starts. Work claimed
before pause may still return and run; existing leases can renew, complete and
retry. Pausing does not cancel handlers, freeze lease clocks or reset attempts.
A claim while paused still detects expired leases and can emit an exhaustion
webhook, but does not lease those messages again.

Resume preserves the cursor, snapshots, retry counts and delays. Channel retention
continues during pause; long pauses can require explicit gap recovery. Replay,
resync and deletion remain separate administrative operations. The inspector shows
paused status and its timestamp. Node helpers keep polling empty paused responses
and automatically resume claiming; the isolated worker client exposes `pause()`
and `resume()` for credentials granted those operations.

Apply `20261012102700481_realtime_consumer_group_pause.sql` before activating the
updated server or issuing pause/resume grants. Downgrade is blocked if credential
records still contain the new grants or last-operation values. Runtime/migration
and UI checks remain pending. See ADR-1035.

## Drain consumer-group leases

Start with account `POST .../groups/{group}/drain` (admin scope and MFA) or worker
`POST /v1/realtime/workers/groups/{group_id}/drain` (explicit `drain` grant). The
response includes `draining`, `drained`, `active_leases` and `paused_at`. Group
status and the inspector show draining/drained. Starting again is safe and
preserves the pause timestamp. Existing credentials gain no new grant.

Drain stops new claims while existing leases can renew, complete or retry.
`drained` means no unexpired leases remain; ready/delayed/dead/unassigned messages
are preserved. Expired handlers may still perform external effects. Stop old
worker processes and ensure handlers exit before replacing them; use idempotent
or fenced effects. The status is an observation, not a processing receipt.

Drain stays active after completion. Resume explicitly to restart claims;
ordinary pause cancels drain mode while keeping claims stopped. Retention and
lease clocks continue, and renewing workers can extend drain beyond a timeout.

Node `drainRealtimeConsumerGroup({ slug, id, channel, group, timeoutMs, signal })`
and `drainRealtimeConsumerGroupWithCredential({ client, timeoutMs, signal })`
initiate once and poll status. The default timeout is 30 seconds across initiation,
status requests and polling; `pollIntervalMs` defaults to one second. Scoped
credentials require `drain` and `status`. Helpers report observed cancellation by
pause/resume. Timeout, cancellation and errors stop waiting without resuming the
group; inspect status after uncertain outcomes. Generated Python exposes initiation
and status APIs for custom deployment tooling.

Apply `20261012102700484_realtime_consumer_group_drain.sql` before activation.
Downgrade blocks incompatible drain credential records and preserves paused state.
Runtime/migration and inspector checks remain pending. See ADR-1036.

## Long-poll consumer-group claims

Single and batch claim bodies accept `wait_seconds` from 0 to 20, default 0.
A waiting request returns when it obtains work, observes pause/drain or a blocker,
reaches its wait deadline, or is cancelled. Nonempty batches return immediately
without waiting to fill. Existing single 204/error and batch empty/partial response
shapes remain unchanged. Configure client/proxy timeouts above the wait plus overhead.

This preview checks availability every 500 ms. It releases its database transaction
between attempts, so there is no held row lock or connection during sleeps. Each
attempt rechecks scoped worker authorization; committed attempts count separately
in credential usage. This reduces HTTP polling, but database polling remains.
At most 64 requests wait per apid process; excess waiting requests receive 429.
Immediate claims remain available. The cap is per process, not fleet-wide.

The deadline bounds sleeps; a database attempt is governed by the parent request
budget and can extend duration. Cancellation releases the waiting slot. If a claim
committed before cancellation or a lost response, its unobserved lease expires
normally. Do not automatically retry uncertain claim outcomes. Use idempotent
handlers and continue respecting pause/drain and retention recovery semantics.

Node helpers default to `waitSeconds: 10`; choose 0 for immediate polling, or up to
20. Empty-result backoff still applies, including paused groups. Direct worker
clients accept `claim(worker, waitSeconds)` and `claimBatch(limit, worker,
waitSeconds)`. Generated Node/Python request models expose `wait_seconds`.
Claim routes use the existing 30-second request-budget ceiling; shorter caller
deadlines still apply. No new migration is required; runtime verification remains
pending. See ADR-1037.

## Consumer-group lag alerts

Read or replace one policy with GET/PUT
`.../channels/{channel}/groups/{group}/lag-alert`. Read requires app read access
and MFA; PUT requires admin scope and MFA. For example:

```json
{"enabled":true,"unassigned_threshold":"100","oldest_pending_seconds":60,"dead_letter_threshold":1,"cooldown_seconds":300}
```

Zero disables each threshold; enabled policies require a nonzero metric. Backlog
accepts decimal strings up to 1000000000, age up to seven days, dead-letter count
up to 128, and cooldown 30–86400 seconds (default 300). Breach means any configured
metric meets/exceeds its threshold; recovery means all configured metrics clear.
Identical PUT preserves state. Changed configuration rearms the policy and clears
observations. Disable silences future evaluation without synthesizing recovery.

Subscribe app webhooks explicitly to `realtime.consumer_group.lag_exceeded` and
`realtime.consumer_group.lag_recovered`. Signed events include app/endpoint/group
IDs, group name, channel, decimal-string `unassigned`, oldest pending age, dead
count, thresholds, pause/drain flags and occurrence time; no message bodies or
credentials. Existing delivery retries apply; deduplicate delivery IDs. Ongoing
breach reminders obey cooldown; actual breach/recovery transitions alert promptly
and can recur faster during flapping. `last_event_at` is an event decision even
when no receiver subscribed. New subscriptions do not backfill prior events.

The inspector shows policy status and observations for the selected group.
`observed_at` and metrics are null until evaluation. Monitoring continues through
pause/drain; endpoint/account/app/plan ineligibility suspends evaluation and can
leave older observations. Unassigned backlog includes unrecovered sequence gaps;
oldest age uses original timestamps of ready/leased snapshots and retained
unassigned events, excluding dead snapshots. Exhausted expired leases count toward
dead letters before a claim materializes them. These are sampled observations.

Preview evaluation runs every 15 seconds, at most 64 due groups per pass with a
20-second pass budget. Replicas coordinate through group/policy locks and durable
next-check times. Busy fleets, contention or outages can delay observations. Group
deletion removes its policy; already queued deliveries follow existing controls.

Apply `20261012102700487_realtime_consumer_group_lag_alerts.sql` before activation.
Runtime, migration and UI checks remain pending. See ADR-1038.

## Per-entity consumer-group ordering

Create a group with `ordering_metadata_key: "job_id"` and
`dead_letter_policy: "block"` (default), or `"skip"`. Empty ordering field keeps
ordinary concurrent behavior; existing groups remain unordered. The field must
be a valid exact metadata key (lowercase letters, digits, underscore, hyphen or
period, at most 64 bytes). Values are case sensitive; missing/empty values share
one bucket. Policies are immutable: choose a new group/cursor to change them.

For one key, only the earliest eligible event can lease and only one lease can be
active across all workers, replicas and batches. Delayed retries hold later events
for the same key. Different keys can run concurrently using existing SDK concurrency
options. Completion releases the key; retry preserves its position. Under `block`,
a dead predecessor holds later events until replay/repair. Under `skip`, later
work proceeds; replay can then run older failed work after successors completed.
Skip retains dead snapshots, which still consume group capacity.
Replay conflicts while another same-key lease is active. Effects still need
idempotency/fencing because expired handlers may continue running.

The inspector and group status show policy and `ordering_blocked` ready count.
Ready counts include blocked snapshots. Batch `ordering_blocked: true` indicates
normal backpressure; helpers keep waiting, and empty single claims return 204.
Long polling rechecks when predecessors settle. Assignment looks ahead within the
existing 128-snapshot window; a hot or dead-blocked key can fill it and prevent
reaching independent keys further ahead. Repair/replay the predecessor rather
than expecting unbounded discovery. Retention-gap recovery remains explicit.

Ordering keys are captured with the message snapshot. Before assignment, retained
metadata edits can affect its key; afterward edits do not change the snapshot.
Missing IDs are serialized in the shared bucket, so publish the configured metadata
consistently. Pause/drain and lag alerts continue to apply to blocked work.

Apply `20261012102700490_realtime_consumer_group_ordering.sql` before activation.
Downgrade removes ordering guarantees; stop ordered workers before reverting.
Runtime, migration and UI checks remain pending. See ADR-1039.

## Lease fencing tokens

Every new consumer-group claim includes immutable `group_id` and a positive
`fencing_token` decimal string. The group's persistent counter increases across
completion, retries, expiration and dead-letter replay; renewal preserves a token.
Group status and the inspector show `last_fencing_token`, initially `"0"`.
Numbers do not wrap at the signed 64-bit ceiling. Existing opaque `claim_token`
continues to authorize lease operations; fencing numbers are public, not credentials.

For ordered workers, enforce the highest fence per downstream entity and group
UUID atomically with each write. For example, with a downstream row already bound
to the authorized consumer group:

See the [customer-storage fencing example](../realtime-customer-examples.md#lease-fencing-in-customer-storage). These writes belong to the application database, not platform operator runbooks.

Initialize that row's fence to zero; check affected rows and reject stale writes.
The group UUID must already be authorized for that resource, rather than accepting
an arbitrary incoming namespace. Register/enforce higher tokens before effects.
A newer Gregale claim alone cannot block an old writer until downstream storage
records its higher token. Equality can support multiple writes from one claim;
separate idempotency controls are still needed for repeated effects. All writers
must participate. Keep tokens as strings or BigInt, not JavaScript Number.

Do not compare unrelated groups or use one global downstream high-water mark for
concurrent independent entities. Recreating a group changes its UUID and requires
an explicit downstream ownership transition. Fencing does not prove expiry or
handler termination and cannot fence an external service that ignores the token.

Apply `20261012102700492_realtime_consumer_group_fencing.sql` before activation.
Legacy issued leases have no retroactive number. Drain/stop old workers, upgrade
all API replicas and clients, then use fresh claims with downstream enforcement.
Downgrade refuses to remove counters from groups that issued tokens; retaining
schema supports code rollback. Coordinate restored counters with downstream state.
Runtime, migration and UI checks remain pending. See ADR-1040.


## Delayed publishing in the inspector

The realtime inspector now includes scheduled events for the selected channel:
pending, paused and failed counts, delivery times, next retry, recurrence,
last error, current version and last published sequence. Channel discovery also
includes pending/paused schedules and terminal schedules from the last 24 hours,
even before the channel has any published history. Discovery shows at most 32
channels; enter a channel name directly to inspect another. Schedule lists are
bounded by the existing endpoint capacity of 256, shared across channels.

Use the existing account-owned schedule API for a one-time delayed event:

```http
PUT /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/reminder-123
Content-Type: application/json
Authorization: Bearer <account-token>

{"data_base64":"aGVsbG8=","binary":false,"deliver_at":"2026-10-12T12:00:00Z"}
```

Choose a future RFC3339 time within 30 days. The schedule ID is the idempotency
identity: retry with the same ID and identical contents to get the existing record;
changed contents conflict. Pending/paused records remain available; terminal
receipts remain available for 24 hours. Reusing a pruned terminal ID can create a
new schedule, so keep IDs unique beyond that receipt window.

Cancel a pending or paused schedule using the current returned version:

```http
DELETE /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/schedules/reminder-123?expected_version=1
Authorization: Bearer <account-token>
```

Cancellation races publication under a row lock. If publication wins, cancellation
conflicts and cannot retract the published message. Use PATCH with `deliver_at`
and `expected_version` to reschedule pending work. Account writes require the
existing deploy-write scope and MFA; scoped publisher tokens cannot manage schedules.

The existing Node helper `realtimeScheduledEvent` builds the schedule request;
Node/Python generated SDKs expose put/list/reschedule/cancel operations. Scheduling
reserves no channel sequence. Publication enters retained history atomically with
the schedule outcome when due, followed by best-effort live fanout. The existing
five-second dispatcher can be delayed by outages, capacity or validation failures;
clients recover missed fanout through retained replay. Schema/reducer checks occur
at release. Inspector reads reveal no schedule payload or credential secret and
provide observation only; use the schedule API for mutations.

## Priority-aware consumer groups

Opt in when creating a new group:

```json
{"name":"notifications","after_sequence":"0","priority_metadata_key":"priority"}
```

Publish metadata such as `{"priority":"9"}` for urgent messages and
`{"priority":"0"}` for routine work. Accepted priorities are exact single digits
0 through 9. Invalid or absent values use zero. The field is immutable; create a
new group to change it. Frozen snapshot priorities survive retry and replay.
Existing groups keep sequence order by default.

Within the bounded 128-snapshot window, eligible messages are selected by descending
priority with sequence as a tie-breaker. Every eighth successful lease instead
selects the oldest eligible message, shared across workers and batches. This gives
older eligible routine work regular turns while urgent traffic continues. Entity
ordering still requires predecessors to settle; priority never bypasses retry
availability, pause/drain, leases, capacity, rate limits or delivery deadlines.
Urgent messages outside the assignment window cannot jump into it. Long scans and
filter skips use the existing batch scan budget and recovery hints.

GET the group's `/priority` route to inspect its field and backlog by priority,
or select the group in the realtime inspector. Ready counts include delayed and
blocked snapshots; leased counts include expirations not yet maintained. Unassigned
channel history is excluded. `next_claim_oldest` is an observation, not a reservation.
Account reads require ownership, read scope and MFA. Worker claim permissions
already cover priority selection; no extra worker grant is required.

Apply migration `20261012102700515` and upgrade all apid replicas before creating
priority groups. Runtime/migration checks remain pending.

## Batch settlement

POST account group `claims/settle/batch` or scoped worker group
`claims/settle/batch` to complete, retry or renew up to 32 claims together:

```json
{"settlements":[
  {"sequence":"42","claim_token":"<claim-token>","action":"complete"},
  {"sequence":"43","claim_token":"<claim-token>","action":"retry","failure_code":"dependency_unavailable"}
]}
```

Results stay in request order, echo sequence/action, and report `completed`, `ready`,
`dead`, `leased` or `lease_lost`. A lost claim does not prevent the others from
settling. Invalid input or duplicate numeric sequences rejects the request before
mutation; database errors roll back the transaction. Retry policy, omitted versus
explicit retry delay, deadlines, lease renewal and per-item processing metrics are
identical to single settlement. Body size is capped at 16 KiB.

Account writes require ownership, MFA and deploy-write scope. Scoped workers need
all requested action grants (`complete`, `retry`, `renew` as applicable), checked
against the fresh credential under the group lock; no new grant is introduced.
Each lease remains bound to its token and issuing credential. One batch counts as
one credential transaction use with its first action as `last_operation`.

Generated SDKs expose `settleRealtimeConsumerGroupBatch` and
`settleRealtimeWorkerGroupBatch`; isolated Node workers have `client.settleBatch`.
Opt-in helper coalescing uses `settlementBatchSize: 16` and optionally
`settlementFlushMs: 10`. Default batch size 1 preserves single settlement.
Complete/retry waits at most the configured timer target (0..25 ms, default 10);
renewals remain immediate. Timer scheduling/network congestion can delay actual
transmission. Leases keep renewing and deadlines remain observed during batching.
Handler slots stay occupied until settlement finishes, so low concurrency can
produce small batches. Processing latency measurements include the batch wait.

A lost response can hide successful settlement. The helper stops on uncertain or
lost-lease outcomes instead of automatically resubmitting; valid peers in a batch
may already have committed. Cancellation cannot undo server commits or handler
side effects. Keep handlers idempotent and use fencing for external writes.
No new migration is required for this addition; runtime validation remains pending.

## Automatic processing circuit breaker

Configure the account group `/circuit` route using PUT (ownership, admin scope and
MFA), or use the generated SDK configuration operation:

```json
{"enabled":true,"failure_threshold":5,"cooldown_seconds":30}
```

Defaults are disabled, threshold 5, cooldown 30 seconds. Threshold range is 1..100;
cooldown 1..3600. Identical writes keep state. Changing any field resets closed,
clears the failure streak and probe, and leaves manual pause/drain and leases alone.
GET uses read scope and MFA. The realtime inspector shows current policy/state.

While closed, successful completions reset consecutive failures; reported retry or
dead failures and lease expiry increment the streak in settlement/observation order.
Deadline expiry and invalid deadlines do not normally count. At threshold, new
claims stop for the cooldown. Already leased work can still renew/settle and may
still become dead-lettered; the breaker does not cancel handlers.

After cooldown, a claim lazily moves to `half_open` and can lease one eligible probe.
The probe uses normal attempt, retry, rate, priority, ordering, capacity, deadline
and lease rules. Its matching sequence/fencing token identifies recovery. Success
closes the breaker; failure or expiry reopens for another cooldown, including a
probe's deadline expiry. Renewal can keep a probe active. Other in-flight outcomes
cannot close an open/half-open breaker. If a probe snapshot was explicitly removed,
the next claim reopens with reason `probe_missing`. Manual pause/drain blocks probes
until resumed. No probe is issued merely because time passed or a status was read.

Single claims return 429 `realtime_group_circuit_open` and Retry-After. Batch claims
return `circuit_open` and `retry_after_milliseconds`, possibly alongside a probe
claim. These fields describe observed admission, not a dependency health check.
An active probe uses a one-second polling hint so recovery need not wait for its
full lease duration. Node helper workers wait on these hints automatically. A group protects its entire
processing path; isolate workloads if unrelated dependencies should have separate
breakers. Per-message dead-letter limits and handler idempotency still apply.

Subscribe app webhooks explicitly to:

- `realtime.consumer_group.circuit_opened`
- `realtime.consumer_group.circuit_half_open`
- `realtime.consumer_group.circuit_closed`

Signed transition payloads include group/channel/endpoint/app IDs, state, reason,
enabled policy, consecutive failures, threshold/cooldown, next_probe_at and time.
They exclude message data and secrets. Policy changes can emit closure with reason
`configured`. State/outbox commit atomically; queued delivery can lag the latest
state. Newly enabled webhook recipients do not receive historical transitions.

Apply migration `20261012102700517` before upgrading the server, then upgrade all
apid replicas before enabling policies. Runtime validation and migration application
are pending; no deployment has been performed.

## Delivery-attempt timelines

Use GET group `/messages/{sequence}/history?after_id=0&limit=32` (account ownership,
read scope, MFA and retained preview) or the generated SDK operation
`getRealtimeConsumerGroupDeliveryHistory`. Choose a consumer group and delivery
sequence in the realtime inspector to browse the same timeline and next page.

History records claims, retries, lease expiration, dead letters, deadline failures,
completion and replay, with observation timestamps, attempts, fencing tokens and
safe failure codes. Renewals are omitted. Dead lease/deadline failures can produce
both their cause and a `dead_lettered` event. Retry/replay retains the snapshot's
fencing context; the next claimed attempt receives a new fence. Replayed rows show
reset attempt zero. Completion remains inspectable after its snapshot is removed.
No payload, metadata, worker identity, credential token/hash or lease token is stored.

Each group keeps at most 1115 events shared across all messages for 24 hours.
Heavy traffic can evict a message's observations sooner. Reads and new observations
prune expired history; idle physical rows stay bounded until cleanup is triggered.
`group_pruned_events` is a decimal lifetime total across the whole group, not proof
that any particular message lost history. Group deletion removes its timeline;
resync keeps existing diagnostic history with the same group identity.

Use decimal string IDs and the exclusive `next_after_id` cursor, with limit 1..100.
`has_more` indicates another retained page. Event IDs may have gaps for a message,
and timestamps reflect observation time: lease/deadline expiration can be detected
later during lazy maintenance. Pagination is observational and pruning can remove
records between requests. Keep large IDs/fencing tokens as strings or BigInt.

Empty history is inconclusive: a message may be unattempted, filtered, completed
before this feature, or pruned. This is bounded troubleshooting data, not a complete
audit or exactly-once proof. Administrative snapshot removal and failed/stale
settlements do not appear as successful delivery. Scoped workers do not get history
access or a new permission.

Apply migration `20261012102700520` before the new server and upgrade all apid
replicas for completion coverage. Runtime tests, UI rendering, migration application
and deployment remain deferred.

### Cooperative lease release

Single and batch settlement accept `action: release`. Scoped credentials need the
explicit `release` permission. Supply the current sequence and claim_token; expired,
stale or differently owned claims follow existing lease-loss responses. Release
returns `status: ready` and `available_at`, clears ownership, refunds the claim attempt,
and records `released` in delivery history. It does not increment retry metrics or
circuit failures. The next claim gets a new fencing token; deadlines, ordering and
claim admission policies still apply.

Node `consumeRealtimeConsumerGroup` and scoped worker helpers support
`releaseOnShutdown: true`. Abort the supplied signal, then await the worker promise.
The helper awaits each handler before releasing, bypasses cancelled settlement
batches, and gives each release request five seconds. Release failures reject the
shutdown promise; no automatic retry is attempted. Internal failures and known lease
loss keep lease-expiry recovery. Handlers must await all work and stop cooperatively;
release cannot undo writes or safely recover detached side effects.

Apply migration `20261012102700522_realtime_cooperative_lease_release.sql` before
rolling out the binary. Migration execution and runtime validation remain deferred.

### Settlement receipts and safe retries

Single settlement accepts an optional UUID `idempotency_key`; batch settlement uses
one top-level key for the entire ordered request. Omit keys on individual batch items.
Repeat the same request with the same key to recover a lost settlement response.
The server returns the original result for 24 hours without repeating settlement,
metrics or delivery-history entries. Reuse with different contents returns
409 `realtime_settlement_key_conflict`. Current ownership/permissions still apply;
worker receipts are scoped to the credential identity. Saved renewal expiry is the
original expiry, so use a new key for each new renewal.

Receipts are limited to 1115 per group across all principals. Full capacity returns
429 before mutation; existing-key replay still works. Live receipts are never evicted,
replays do not refresh expiry, and expired rows are pruned on keyed requests. Keys
have no replay protection after expiry. Requests without keys keep existing behavior.
Receipts store result JSON and a request fingerprint, with no raw claim tokens.

Node processing helpers support `settlementRetries: 2` (0–3, default 0). This generates
one UUID for each terminal single request or whole batch and retries transport errors
and HTTP 408/500/502/503/504 using the same contents/key. Auth, validation, conflicts,
429 and cancellation do not retry. Renewals are not automatically retried. Combined
with `releaseOnShutdown: true`, release retries stay within the five-second request
budget. Handler side effects still require idempotency or atomic fencing.

Apply `20261012102700527_realtime_settlement_receipts.sql` and roll out all API replicas
before enabling SDK retries. Downgrade requires live receipts to expire first.
Runtime tests and migration execution remain deferred.

### Consumer-group event deduplication

Create a group with `deduplication_metadata_key: event_id` to suppress additional
published copies of the same logical event. `deduplication_window_seconds` defaults
to 3600 when enabled; choose 60–86400 explicitly. The settings are immutable, and
existing groups stay disabled. Without a key, the window must be zero. IDs compare
exactly; missing/empty values process normally. Use distinct IDs for distinct logical
events and intended corrections. The first filter-matching assigned copy wins,
including copies admitted into dead-letter state; payload differences are not compared.

Duplicates advance the source cursor without allocating snapshots or leases.
`duplicate_messages` counts skipped copies; `deduplication_keys` counts live hashes.
Both appear in group status and the inspector. Filtering runs first and does not
reserve IDs. Existing snapshots retain retry, release and explicit dead-letter replay
behavior. Deduplication does not establish successful processing or fence side effects.

At most 4096 IDs per group are remembered until their window expires. Duplicate hits
do not extend expiry, and live IDs are never evicted. A new ID at capacity waits without
advancing its cursor; already remembered duplicates can still skip and existing
snapshots remain claimable. Single claims return 429 `realtime_group_deduplication_full`
with a retry hint; batches use `deduplication_blocked`, may contain partial claims,
and include `retry_after_milliseconds`. Node processing polls within these admission
hints using `pollIntervalMs` (default one second) to discover existing ready snapshots.
Assignment still scans at most 128 source events per request. A blocked new ID can
hold later source events. After expiry, another copy can be assigned again; resync
preserves remembered hashes until expiry. Group deletion clears them.

Apply `20261012102700530_realtime_consumer_group_event_deduplication.sql` and upgrade
all API replicas before creating enabled groups. Downgrade cannot restore skipped
source events. Runtime tests, inspector rendering and migration execution remain deferred.

### Dead-letter JSONL export and external archival

GET `/v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/groups/{group}/dead-letters/export`
returns a JSONL attachment. It requires account ownership, read scope, MFA and the
history preview gate. The inspector's Download dead letters as JSONL action uses
the same checks; worker credentials cannot export.

The first line is a versioned manifest. Subsequent lines contain retained payloads
(base64 data and metadata), failure code, attempt count, dead_since and fencing/
deadline/ordering context. No claim tokens or credentials are included. Export is
read-only: it does not claim, replay, clean up, extend retention or update counters.
Save the downloaded files externally when a longer archive is needed.

Default page size is 32; `limit` accepts 1–128. Repeat `failure_code` for exact filters
and use `after_sequence` for an exclusive cursor. Resume with the manifest's
`group_id`, `through_sequence`, `snapshot_at` and `next_after_sequence`, retaining the
same filters. These resume identity/boundary/cutoff fields must be supplied together.
The `Link` header points to the next page; `X-Realtime-Export-*` headers mirror cursor
metadata. Empty results still contain a manifest. Verify a complete transfer and
manifest record count before advancing a saved cursor.

Each page is consistent within a read-only repeatable-read transaction. Between pages,
replay or retention cleanup can remove failures; later failure transitions are excluded
by the original cutoff. Export is not a frozen server archive or a recovery/import
endpoint. For periodic archival, start fresh from sequence zero and deduplicate on
`group_id`, sequence and `dead_since`; old sequences can fail again after replay.
Group recreation or a rewound export boundary returns 409
`realtime_dead_letter_export_changed`; restart the export deliberately.

No new migration is required. Runtime tests and inspector rendering remain deferred.


### Background dead-letter replay jobs

Use `POST .../groups/{group}/replay-jobs` with `rate` (1–32), optional `failure_codes`, `after_sequence` (exclusive) and `through_sequence` (inclusive). Creation captures current failures only, up to 128; an empty selection is invalid. `GET .../replay-jobs` shows active jobs first and recent terminal jobs. `POST .../replay-jobs/{job}/{action}` accepts `pause`, `resume` or `cancel`. Account ownership and MFA apply; mutations require deploy-write scope. The inspector offers the same controls.

Rate is an upper limit per job; multiple jobs have independent limits. Known outcomes include replayed, stale, missing, not_dead, ordering_blocked, invalid_deadline and deadline_expired. Skips require a new recovery job. Safe processing errors and cumulative counts appear in status; database errors retry without advancing progress. Cancellation does not undo completed replay. Inspect recent jobs before retrying an uncertain creation response. Disabled endpoints suspend execution; group claim pause/circuit/rate settings still govern subsequent processing.

Apply migration `20261012102700533_realtime_dead_letter_replay_jobs.sql` before enabling jobs, with all API replicas upgraded. Cancel active jobs before downgrade. Migration and runtime/UI checks are currently deferred.


### Replay-job notifications

Create or update an app webhook with event filters `realtime.replay_job.completed`, `realtime.replay_job.cancelled` and/or `realtime.replay_job.processing_failed`. Existing webhook signing, retry policy, delivery status and dead-letter inspection apply. The inspector links to app webhook configuration.

Completion and cancellation notify once per transition. The processing warning notifies once per job when cumulative recorded processing errors first reach three; the job continues retrying. Known skips are not processing errors. No backfill for past transitions or newly added subscribers. Deduplicate the payload's `event_id` across delivery retries and do not assume delivery order.

`RealtimeReplayJobWebhookPayload` documents recovery counts, safe error diagnostics and a relative `inspector_path` to resolve against your console origin. Payloads exclude failed messages and credential secrets. Job history can be pruned after notification. Migration `20261012102700536_realtime_replay_job_notifications.sql` is required and currently unapplied.

### Dry-run replay jobs

`POST .../groups/{group}/replay-jobs/dry-run` accepts the same rate and failure-code/sequence filters as job creation. It captures failure identities, checks eligibility and stores a report without expiring leases or mutating the queue. Dry runs use `checking`/`check_paused`; pause, resume and cancel work normally. Both modes share active-job quotas and bounded history. Disabled endpoints suspend checks. Dry runs do not emit replay lifecycle webhooks.

Inspect `dry_run`, `eligible`, `skipped` and per-item `checked_at`/status. Results reflect individual observation times and can change. `POST .../replay-jobs/{report}/start` creates a real replay from only the report's eligible identities, preserving its rate and rechecking every item before mutation. The source report gains `started_job_id`; the new replay has `reviewed_job_id`. Repeated start returns the same replay while retained; a pruned replay returns 409 and a pruned report 404. It never silently starts a second recovery from one retained report. Create a new dry run to reassess skipped failures.

Apply migration `20261012102700538_realtime_replay_job_dry_runs.sql` and upgrade all API replicas before enabling the feature. It is currently unapplied. Before downgrade cancel active dry runs; terminal dry-run reports are discarded on downgrade. Real replays continue under existing claim pause/circuit/rate controls.

### Python worker lifecycle

The Python SDK exports account and scoped-credential async consumer-group helpers in `faas_sdk.realtime_workers` and at the package root. They handle bounded concurrent batch claims, lease renewal/expiry, handler complete/retry settlement and optional readiness heartbeats. Supply an async handler that awaits every effect and observes its context stop event; enforce idempotency and fencing at downstream writes. Callers own the async HTTP client and must disable custom mutation retries.

Use an asyncio.Event for graceful shutdown. Opt-in release waits for handlers and lease monitors to stop and only releases valid leases after caller shutdown. Task cancellation joins the same owned tasks before propagating. Expired/lost leases and uncertain terminal settlements are not followed by release. Python `settlement_retries=0` is the default; opt in to 1–3 receipt-backed retries for complete/retry/release on temporary network errors or HTTP 408/500/502/503/504. Each logical settlement reuses one UUID key and body, with 100/200/400 ms backoff. Claims and renewals never retry automatically. Cancellation interrupts requests/backoff; release still has a total five-second budget. Auth, validation, conflicts, capacity and malformed responses stop retries. Requires server settlement receipt support (ADR 1055). Scoped workers require status/claim/renew/complete/retry grants and optional heartbeat/release grants. SDK runtime validation remains deferred; this addition needs no migration.

### Python worker settlement batching

Set `settlement_batch_size=2..32` to coalesce completed/failed handler settlements (default 1 is disabled). `settlement_flush_milliseconds=1..100` defaults to 10. The queue retains handler concurrency slots; size or delay triggers a batch. Renewals and shutdown releases bypass batching. With receipt retries enabled, one top-level UUID covers the exact ordered batch, with no item keys. Validate all item identities/actions/statuses before notifying handlers; confirmed successes survive another item's lease loss. A lease-lost item stops further claims.

Graceful caller shutdown flushes queued terminal settlements and awaits batches; each batch has a five-second total request/retry budget. Internal failures cancel in-flight batches and discard unsent entries. Queued or uncertain terminal requests never lead to release. Flush delay can exceed remaining lease time; expiry is reported rather than replayed. No server migration is added. Runtime validation remains deferred.

### Python worker lifecycle monitoring

Set `RealtimeWorkerOptions(on_event=record_event)` with a synchronous callback taking exported `RealtimeWorkerEvent`. Events report claims, confirmed complete/retry/release settlements, lease loss (once per claim) and final shutdown. Claim identifiers are group UUID/sequence/attempt; monotonic duration covers processing through settlement or loss. Shutdown reasons are stopped/cancelled/failed/blocked. Events exclude payloads, metadata, worker identity, fences, claim tokens, credentials and exception details.

Enqueue into a bounded nonblocking monitoring queue; callback exceptions are ignored and asynchronous callbacks are not awaited. Blocking callbacks still block the event loop, so keep them short. Successful batch items emit independently, receipt retries do not duplicate notifications, and uncertain requests have no settlement confirmation event. Shutdown fires after task cleanup. This is best-effort local telemetry, not durable history; avoid high-cardinality sequence metric labels. No migration is added; runtime validation remains deferred.

### Python worker processing timeout

`processing_timeout_seconds=None` disables processing limits by default. Opt in with a finite value 0.1–86400 seconds. The monotonic budget starts at handler invocation and excludes settlement time. Timeout sets the handler context stop event, cancels renewal and emits one `handler_timeout` lifecycle event, while continuing to await the handler's safe exit. Retry a still-valid lease with reserved `handler_timeout` code, bypassing the custom failure classifier; existing retry policy/batching apply. Confirmed retry/dead results notify `on_handler_error` with TimeoutError.

Keep lease/deadline monitoring active. If expiry occurs before safe handler exit, stop the worker without a known-expired settlement. Do not force-cancel or release a running handler. Ignoring stop can still hold a slot indefinitely, and event-loop blocking delays timeout signalling. An accepted renewal cannot be undone by cancellation. Caller shutdown retains normal safe-release rules. No migration is needed; runtime checks remain deferred.

### Python worker deployment draining

Supply a separate `drain_event` asyncio.Event to either Python worker entry point. Setting it permanently stops new claim requests for that invocation. Keep lease renewal, active handlers, processing timeouts, heartbeats and settlement running; flush pending batches immediately. An already-issued claim completes normally and its returned leases join owned work. Successful drain exits after every owned task/settlement completes, emitting draining then shutdown/drained lifecycle events. An already-set drain returns without network calls.

Drain does not signal handler context stop events, release healthy work, pause the server group or empty the whole backlog. Clearing the signal does not resume consumption. Urgent stop/task cancellation overrides draining; internal/lease failures and retention/capacity blockers remain errors. No automatic drain deadline is imposed; use existing stop signalling and processing timeouts as needed. No migration or runtime checks are added.

### Python expiring activity tracker

Existing managed realtime presence/signals already provide server-side ephemeral activity and principal-scoped multi-session grouping. Python `RealtimeActivityTracker` consumes named authenticated signal frames through `RealtimeActivity`, keyed by channel/member/name. It expires state with an asyncio timer, preserves nanosecond timestamp ordering, ignores duplicates/stale revisions, handles zero-TTL clears and retains bounded 30-second tombstones. Default capacity is 1115 identities including tombstones (configurable 1–8192); TTL is at most 30 seconds and JSON data at most 2048 UTF-8 bytes.

Construct/use on one running asyncio loop. Supply a synchronous nonblocking on_change callback; exceptions are ignored. Wire presence departure to remove_member, reconnect/unsubscribe to reset, and client shutdown to dispose. Feed the verified server member_id unchanged; principal-scoped subscriptions group sessions while connection-scoped subscriptions keep them separate. Synchronize clocks because updates outside a 30-second clock window are ignored. This helper opens no connection, issues no presence lease and changes no authorization. No migration or runtime checks are added.

### Python combined presence directory

`RealtimePresenceDirectory` owns an expiring activity tracker and reports combined `RealtimePresenceSummary` snapshots through one synchronous callback. Feed verified presence frames as RealtimePresenceEvent/RealtimePresenceMember and named signals via apply_activity. Member/connection counts stay distinct; principal-scoped member IDs aggregate sessions. State is presentation data, not verified identity. Unknown activity senders are excluded from summaries.

Stage incomplete snapshot chunks until complete=True, apply live changes to visible and staged membership, and remove departed members' activity on leave or completed replacement. Bound visible plus staged entries and channel count with max_members (default 1115, range 1–8192), and activity independently with max_activities. Invalid or overflowing presence events do not commit replacement state. Reset on transport loss/reconnect/unsubscribe; dispose at shutdown cancels timers. Presence follows server events rather than a local guessed TTL. No migration or runtime checks are added.

### Python temporary activity publishing

RealtimeActivityPublisher uses an application-provided authenticated async sender to coalesce latest values and refresh named activity at half TTL. Default TTL 5000 ms (100–30000) and interval 250 ms (50..half TTL); data stays within 2048 JSON UTF-8 bytes. It owns one refresh/write task, bounds writes to five seconds and serializes clear after an active write under a five-second total budget. Explicit clear sends null/TTL zero. Close clears by default or cancels locally with send_clear=False.

Transport failure stops refresh and reports a sanitized error through error/on_error; it does not retry ephemeral sends. Supply a cancellation-cooperative sender and await close before closing the transport. Activity remains active until clear/close or a configured idle timeout; throttling alone does not infer user inactivity. Slow writes can exceed TTL and cancellation leaves remote results uncertain.

create_managed_realtime_activity_publisher wraps account HTTP channel/scope signal routes. These publish as backend, with existing deploy-write/MFA checks, and do not represent a presence user. Use a standalone activity tracker for backend signals. User typing requires your authenticated user transport sender. No migration or runtime tests are added.

### Python activity publisher idle timeout

Set idle_timeout_seconds on RealtimeActivityPublisher or its HTTP factory to automatically clear after no accepted update calls (finite 0.1–86400 seconds; default None disables). Every update resets a monotonic deadline even for unchanged data; periodic refreshes do not. The existing owned task wakes at the earlier refresh/throttle/idle deadline. On idle, serialize a null/TTL-zero clear after previous writes, then stop. Later updates restart; updates during the clear are retained and sent afterward if still active.

Remote TTL stays independent, providing eventual expiry on failed clears. Slow or blocked sends can delay idle processing; existing five-second write limits, failure callbacks and cancellation cleanup apply. No additional task, server migration or runtime validation is added.

### Python activity presentation

Use summarize_realtime_activity on a directory snapshot/callback to filter named activities, exclude the current member and assign labels. It returns isolated, sorted viewers and active members, plus viewer/session counts. Activity filters retain online viewers; exclusion removes the chosen member entirely. Ignore expired activity at projection time. Labels default to member IDs and are untrusted presentation text, never authorization inputs.

format_realtime_typing_summary produces English typing text with 1–10 displayed labels and an overflow count, returning empty text when no unexpired typing remains. Use active members for localized/editing status UI. These helpers own no timers or transport; directory/activity expiry callbacks drive updates. No backend change or runtime validation is added.

### Python activity-scope encoding

realtime_activity_scope_channel(parent, scope) builds the server's canonical private activity channel using unpadded UTF-8 base64url parts. parse_realtime_activity_scope returns the parent/scope pair for canonical reserved channels, None for ordinary channels, and rejects malformed reserved encodings. Parent/scope byte limits, trimming/forbidden characters, nested scopes and total encoded length match pkg/api/realtime_activity_scopes.go.

Use the canonical channel for subscriptions and directory/tracker input. The HTTP factory continues accepting the original parent channel plus activity_scope and now validates both before requests. Encoding does not grant read_activity permission or inherit parent access; existing server authorization applies. No migration or runtime validation is added.

### Python confirmed read-progress tracker

RealtimeReadProgressTracker tracks server-confirmed channel/inbox receipts by stream and hashed reader ID. It ignores exact duplicates or backward read/latest/oldest bounds, preserves history_unavailable, and accepts equal bounds when unread/count status changes after edits/deletions. There is no revision for equal-bound count updates; refresh authoritative read state if your transport may reorder them. Do not derive a user's read position from connection ACKs or mere receipt of a message.

Capacity defaults to 1115 entries (1–8192); malformed/overflow updates fail without replacing state. Immutable snapshots expose server unread counts directly. Reset local ordering on reconnect/stream replacement; durable server markers remain intact. Callback failures are isolated, and no timers, connection, authorization or server writes are introduced. No migration or runtime tests are added.

### Python coalesced read assertions

RealtimeReadProgressPublisher binds one reader/stream to an authenticated sender. mark_read accepts only explicitly viewed positions, retains the highest pending value and ignores duplicate/older calls. A single task serializes writes with configurable 50–30000 ms throttle (250 default). flush bypasses throttle under a five-second total budget; aclose flushes by default or cancels when flush=False. Each write also has a five-second timeout.

No automatic retries or confirmed-state updates are introduced. Failures stop publication, preserve pending diagnostic position and expose a sanitized error/callback. Refresh authoritative state before replacing a failed publisher. Cancellation can leave a remotely accepted write uncertain. Keep pending values separate from RealtimeReadProgressTracker receipts. The HTTP factory uses existing account channel/inbox routes and deploy-write/MFA authorization, with explicit principal assertions. No migration or runtime validation is added.

### Python unread aggregation

Use summarize_realtime_unread on one endpoint's confirmed receipt snapshot with one hashed reader_id. Filter channels and optionally inbox; sum known per-stream unread counts, expose unread-stream counts and history-gap receipts. Other readers are excluded and duplicate stream/reader entries are rejected. Totals are per-stream sums, not globally deduplicated message counts.

Explicit requested channels enable missing_channels reporting; include_inbox=True reports inbox_missing when not loaded. complete is None without an explicit channel selection and otherwise indicates requested receipt coverage only. Missing receipts are not fabricated as zero; complete does not guarantee freshness or gap-free retained history. This helper makes no requests or read assertions. No migration or runtime validation is added.

### Python read-progress freshness

Optional stale_after_seconds (finite 0.1–86400, default None) tracks monotonic age of confirmed receipt arrivals. freshness_snapshot exposes age/stale per receipt; mark_stale immediately flags matching streams on disconnect without discarding counts. Valid duplicates renew freshness; backward sequence bounds do not. reset/dispose clear freshness memory.

refresh_stale returns stale receipts and invokes an optional synchronous on_refresh hook to enqueue authenticated refreshes. It never marks data fresh or advances server read positions. Drive checks from existing UI/transport polling; no timer/network loop is added. Debounce repeated refresh requests in the application.

Pass a current freshness snapshot to summarize_realtime_unread to obtain stale_streams while retaining confirmed totals. Missing/mismatched metadata counts as stale; omitted metadata gives None. Freshness snapshots are point-in-time observations, separate from requested-stream completeness and retained-history gaps. No migration or runtime validation is added.

### Python bounded read-refresh coordination

Wire tracker on_refresh to RealtimeReadRefreshCoordinator.request, with an application-owned authenticated fetcher returning confirmed RealtimeReadProgress. Bound concurrency (default 4, 1–32), total queued/active identities (default 1115, 1–8192) and request timeout (default 5 seconds, 0.1–60). Repeated queued/in-flight identities coalesce; overflowing batches fail admission atomically.

Validate response identity/bounds and apply through the tracker. Reset increments a tracker generation, invalidating all previously admitted refreshes even for a scoped reset. Request errors use an isolated sanitized callback without automatic retries. wait_idle waits for admitted work; aclose cancels/joins requests and drops queued work before transport closure. No automatic poll loop, read writes, migration or runtime checks are added.

### Python unread badge formatting

format_realtime_unread_badge returns structured capped count text, visible text, exact known total, accessible English label and updating/gap/visibility flags. Defaults cap at 99 and hide confirmed zero. Missing coverage or unknown/stale freshness shows Updating…; this display state does not issue refreshes. Retention gaps remain separate and keep confirmed-zero badges visible with a retained-history caveat. Use structured fields for localization. No migration or runtime validation is added.

### Python realtime connection status

RealtimeConnectionStatusTracker observes caller-owned connection attempts through begin_connect, connected(attempt_id) and disconnected(attempt_id). Capture attempt IDs in transport callbacks to ignore events from superseded connections. It exposes immutable connection/reconnect/offline state, successful UTC connection time and reconnect count since success, without implementing another retry layer.

on_invalidate runs before nonconnected status notifications: mark unread data stale and reset ephemeral presence using existing helpers. Connected transport status does not imply refreshed unread counts or a complete presence snapshot. Callbacks must be synchronous, fast and idempotent; errors are isolated. Dispose marks offline and prevents later admission. No network tasks, migration or runtime checks are added.

### Python realtime connection quality

RealtimeConnectionQualityTracker observes current-attempt frames and locally correlated heartbeat sends/responses. It exposes latency, receive silence, oldest unanswered heartbeat age and unknown/healthy/degraded/offline status. Default degradation/recovery thresholds are 1000/500 ms, silence 30 seconds and heartbeat timeout 10 seconds. Recovery hysteresis avoids threshold flicker. Repeated sends preserve the oldest unanswered age; old attempts/probes are ignored.

Drive snapshot from existing lifecycle ticks to detect silence; no timer, heartbeat or reconnect layer is added. Frame receipt does not clear an unanswered probe or high-latency warning. Callback errors are isolated. Degraded is an observation, not proof of outage or a disconnect action. No migration or runtime validation is added.

### Python combined realtime health

Use summarize_realtime_health to project existing status/quality/unread/presence snapshots into one UI state. Connection outage takes priority, then degraded current-attempt quality, then incomplete/stale data; history gaps remain a separate warning. Ignore older-attempt quality rather than reporting it as current. Omitted optional features are untracked.

PresenceSummary.ready distinguishes empty complete snapshots from pending data. Completed snapshots set readiness; chunked refresh marks pending while preserving visible state. Reset clears readiness and notifies even for an empty channel. Health treats nonconnected presence as waiting. Keep reset wired to connection invalidation so prior-attempt presence is not reused. No transport policy, migration or runtime validation is added.

### Python reconnect catch-up observation

RealtimeCatchUpTracker freezes an explicit authoritative target for one channel/inbox and observes successfully processed ordered positions. Subscribe ACK cursors are not targets. Unknown target remains waiting_target; sequence distance is not message count. Local generations reject late callbacks across resubscription, including same-attempt recovery. Server resync-required blocks progress until application recovery starts a new generation.

Pass snapshots into summarize_realtime_health for Catching up/Resync required UI. Older-attempt observations are unknown. This changes no replay, ACK, read-position or recovery behavior. No migration or runtime tests are added.

### Python catch-up stall warnings

Optional stall_after_seconds (finite 0.1–86400, default None) detects no successfully processed progress while below a known replay target. Begin/first target/strict progress advancement restart monotonic timing; duplicate/backward updates do not. snapshot evaluates age, emits the stalled transition and exposes progress_age_seconds plus threshold-exceeding stall_seconds. Progress resumes normal classification.

Use existing ticks, not another timer layer. Health displays Catch-up stalled after connection/resync priority and before degraded quality. Unknown target, completed replay and resync states do not accumulate stalls. Warning changes no retry/ACK/recovery behavior. No migration or runtime validation is added.

### Python subscription readiness

RealtimeSubscriptionTracker observes a bounded requested channel/inbox set with per-stream pending/subscribed/denied/resync-required state. New begin generations reject late callbacks and may replace requirements on the same connection attempt. Reset clears readiness; reestablish the full required set via begin. Missing ACK timeout is optional and observed on snapshot ticks, never classified as denial. No subscribe/authorization/retry policy is added.

Pass summaries to health projection for pending/denied/resync visibility; readiness is separate from connection, presence and processed replay progress. Existing server grants remain authoritative. No migration or runtime validation is added.

### Python subscription readiness waits

wait_ready(generation, channels=..., include_inbox=..., timeout_seconds=...) awaits all or selected already-requested acknowledgements. Terminal denial/resync yields sanitized RealtimeSubscriptionWaitError; new begin/reset makes prior waits obsolete, and disposal terminates them. Success returns a selected ready summary without implying replay/data readiness.

Timeout/cancellation remove only the waiter and do not alter subscription state. Default timeout is None; finite 0–86400 is supported. Bound active waits with max_waiters (128 default, 1–8192), using one asyncio loop. No polling/network/retry layer, migration or runtime tests are added.

### Python catch-up completion waits

wait_caught_up(generation, timeout_seconds=..., fail_on_stall=True) awaits successfully processed replay against its known frozen target. It reports typed stalled/resync/obsolete/disposed outcomes. Configured stall deadlines wake active fail-on-stall waits without an external tick; fail_on_stall=False waits through warnings. Unknown target remains pending.

Timeout/cancellation clean waiter futures only. Bound active waits to max_waiters (128 default, 1–8192), using one asyncio loop. Completion does not imply later live messages or pending effects outside the ordered cursor are processed. No transport/retry/ACK/recovery policy, migration or runtime validation is added.

### Python combined readiness gate

wait_realtime_ready combines current-attempt connection, selected subscription ACKs and up to 32 frozen catch-up targets under one deadline. It observes completed stages until all requirements are ready and rechecks generations/states before success. Structured errors identify connection/subscriptions/catch-up stage and safe reason. Offline, disposal, obsolete generation, denial, resync and optionally stall terminate waits.

Only bounded futures/deadline waits are owned. Cancellation/timeout unregister gate listeners without changing transport, ACK, replay or retry policy. Bind trackers to one endpoint/account/loop. Success is a point-in-time readiness observation. No migration or runtime validation is added.

### Python local realtime diagnostics

export_realtime_diagnostics serializes an explicit whitelist from current health/unread snapshots into version-1 JSON with UTC capture time. Default channel/reader aliases are local to the report; include_identifiers=True is an explicit opt-in. Payloads, presence state, tokens, credentials, transport URLs, exception bodies, arbitrary codes and labels are always omitted. The helper never uploads or reads external state.

Bound arrays with max_entries (256 default, 1–8192) and report truncated counts; total JSON is capped at 256 KiB. Sequences/unread counts use decimal strings. Counts/timestamps remain potentially descriptive even when identifiers are redacted. No migration or runtime validation is added.

### Python realtime incident timeline

RealtimeIncidentTimeline records connection, subscription/ACK-warning and catch-up state transitions from application callbacks into a ring (256 default, 1–8192). Deduplication memory is separately bounded (1115 default, 1–8192) with oldest-observation eviction. Capture UTC plus monotonic elapsed time, safe states and attempt/generation/channel identity only; never copy payloads, tokens, arbitrary codes or error bodies.

Pass timeline to diagnostics export for default channel redaction and newest-event truncation. Distinguish ring evicted_count from export omitted counts. Raw snapshots contain supplied channel IDs; no upload/storage occurs. clear resets local history. No migration or runtime validation is added.

Python reconnect/replay orchestration is available via
`RealtimeRecoveryCoordinator` (ADR 1090). Supply an authenticated session adapter
with ordered replay and bounded live buffering, and optionally an atomic snapshot
loader for retention gaps. Only explicit transient transport failures reconnect;
application failures and operation deadlines stop. Readiness follows full replay,
not connection establishment. Optional incident timelines record recovery states.
See the Python SDK README for the adapter and cursor durability contracts.

The Python `realtime` installation extra adds native `RealtimeWebSocketClient`
(ADR 1091). Supply its `connect` method to the recovery coordinator for a retained
channel or named inbox consumer. It uses v2 server replay, bounded buffering,
processing ACKs and native Ping/Pong; read-state queries capture history bounds
without marking messages read. WSS, verified bearer identity and read-state support
are required. See the SDK README for inbox cursor and snapshot-reset semantics.

Python `RealtimeMultiChannelClient` (ADR 1092) multiplexes up to eight retained
channels over one authenticated native WebSocket. Each channel has independent
completed progress, replay/snapshot recovery, bounded buffering, readiness and
redacted diagnostics. Dynamic unsubscribe confirms wire removal before channel
reuse; uncertain cleanup closes the socket and surviving channels recover.
Connection loss affects the shared socket; application processing failures remain
local to their channel. Inbox subscriptions continue through the dedicated client.

Python native dedicated and multiplexed channel clients support
`RealtimeWebSocketPresence` (ADR 1093): online member snapshots/updates, verified
connection counts, expiring named activity and presence/signal publishing.
Transport/session failure clears local presence and activity before reconnect
rebuilds it. Retained replay readiness and presence snapshot readiness are separate.
Rate-limit/relay rejections are safe callbacks, not retained-stream failures.
Per-channel redacted diagnostics expose presence readiness/counts, excluding state
and signal data. See the SDK README for callback ownership and publisher cleanup.

Python native channel clients support optional named durable subscriptions
(ADR 1094). Set a name on the dedicated client or explicitly per multiplexed
channel. Resume uses the server's principal-scoped checkpoint; confirmed_sequence
tracks persisted ACKs separately from locally completed processing. Retention-gap
snapshots reset/confirm the checkpoint before resubscription. Unsubscribe retains
stored state. Use one logical processor per identity and idempotent effects for
commit/ACK crash windows; a stored cursor cannot rebuild lost application state.

Python channel clients accept immutable `RealtimeEventFilter` metadata selection
(ADR 1095). Ordered skipped checkpoints advance/ACK progress without message-handler
side effects; adjacent skips coalesce in bounded queues while preserving replay
boundaries. Persist local filtered cursors with their fingerprint. Nonempty filtered
durable names derive a distinct checkpoint identity for each selection; this SDK
binding is not enforced by the server for other clients. Filters do not grant
channel access and do not select presence/activity or inbox events. Diagnostics
omit filter values and fingerprints.

Python native channel/inbox clients support `RealtimeWebSocketReadProgress`
(ADR 1096), including explicit mark-read/refresh actions, confirmed unread badges
and selected-principal redacted diagnostics. Supply the verified server reader key;
channel broadcasts can describe other readers. Reconnect refreshes state, while
new events coalesce bounded queries and disconnect preserves stale counts.
Processing/durable/filter ACKs never imply user visibility. Read-action errors do
not stop retained delivery after its authoritative target is established. Combined
refresh and mark-read traffic shares the existing socket read-operation rate limit.

Python native clients support socket-wide live direct messages through
`RealtimeWebSocketDirectMessages` (ADR 1097). A bounded async worker invokes the
handler once per message/socket and confirms requested receipts after successful
processing; retries resend only ACKs. Handler failures stop connection recovery,
while receipt exhaustion reports a safe error. Disconnect cancels/discards live
work, independently of retained/read/durable cursors. Use inboxes when messages
must survive disconnects. Shared clients attach one handler per physical socket.
