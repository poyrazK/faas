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
POST /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/send
POST /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/close
PUT|DELETE /v1/apps/{slug}/realtime/endpoints/{id}/connections/{connection_id}/subscriptions/{channel}
POST /v1/apps/{slug}/realtime/endpoints/{id}/channels/{channel}/publish
```

The API persists endpoint configuration in the control plane, applies the
per-plan inventory cap, returns masked credentials, and mirrors enabled rows to
active realtime nodes. In a single-box install this is the local
`FAAS_REALTIME_SOCKET`; in a multi-node install apid uses each node's private
`gateway_target_url` and the `gatewayd-internal` control proxy. Connection
operations are routed through the leased owner directory, while publish is
broadcast to active nodes. The daemon-socket example below remains useful for
node-local bootstrap and recovery tooling.

Endpoint writes are best-effort fan-out operations. apid performs an immediate
reconciliation at boot and every 30 seconds, replaying enabled rows and
removing disabled rows on active nodes. If a node is restarting or unreachable,
the pass records the failure and retries on the next interval; no endpoint
mutation is required to heal the node after it becomes active.

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
