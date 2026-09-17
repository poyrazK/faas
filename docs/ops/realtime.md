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
broadcast to active nodes. The daemon-socket example below remains useful for
node-local bootstrap and recovery tooling.

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
Use the authenticated API (or `pkg/realtime.Client` for node-local tooling) to
send to a `connection_id`, subscribe/publish channels, or close a connection.
Send and publish bodies contain `data_base64` and an optional `binary` flag;
decoded frames are limited to 1 MiB. The built-in HTTP callback hook retries
transient network failures and 408/429/5xx responses up to three total attempts
with a bounded backoff before reporting a callback error. In multi-node mode, apid discovers and
leases the connection owner, renews the lease for the operation, and retries a
stale owner once. Endpoint registration must be able to reach each node's
private `gateway_target_url`; missing or unreachable nodes remain fail-closed
for connection operations (`503`) and are skipped when another node accepts a
publish.

Inspect health and counters from the `faas` group:

```
curl --unix-socket /run/faas/realtimed.sock http://localhost/healthz
curl --unix-socket /run/faas/realtimed.sock http://localhost/internal/stats
curl --unix-socket /run/faas/realtimed.sock http://localhost/internal/connections
```

The customer CLI exposes the authenticated connection operations as well:

```sh
# Send raw bytes from a protected pipe; add --binary for binary frames.
printf 'hello' | gregale realtime send my-app ENDPOINT_ID CONNECTION_ID --data-stdin

gregale realtime subscribe my-app ENDPOINT_ID CONNECTION_ID room-a
gregale realtime unsubscribe my-app ENDPOINT_ID CONNECTION_ID room-a
gregale realtime close my-app ENDPOINT_ID CONNECTION_ID --reason 'client migrated'

printf '{"event":"refresh"}' | \
  gregale realtime publish my-app ENDPOINT_ID room-a --data-stdin

# Inspect active connections before targeting one for management.
gregale realtime connections my-app ENDPOINT_ID --channel room-a --limit 100

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
messages and bytes, dropped messages, and callback errors. In a split
deployment, the control-plane Prometheus discovers active realtime owners from
apid; sum counters across nodes and sum the current-connection gauge only when
you want a fleet total.

Set `FAAS_REALTIME_MAX_CONNECTIONS`,
`FAAS_REALTIME_MAX_MESSAGE_BYTES`, `FAAS_REALTIME_OUTBOUND_QUEUE`,
`FAAS_REALTIME_HEARTBEAT`, `FAAS_REALTIME_PONG_WAIT`,
`FAAS_REALTIME_WRITE_WAIT`, `FAAS_REALTIME_MAX_AGE`, and
`FAAS_REALTIME_CALLBACK_TIMEOUT` in the realtimed environment file when
adjusting limits. `FAAS_REALTIME_CALLBACK_OUTBOX` optionally overrides the
node-local callback spool (default `/run/faas/realtime-callbacks`). Message and
disconnect events are fsynced before delivery and replayed after a realtimed
restart; delivery is at-least-once, and poison events are retained under the
outbox's `dead/` directory after the bounded retry budget. Keep the callback URL
on an ordinary app route so the normal gateway wake path can start a sleeping
application to process an event.

`/internal/stats` includes callback-pending, callback-pending-bytes, and
callback-dead-letter counters alongside the connection and delivery counters.
