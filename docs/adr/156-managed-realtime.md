# ADR-156 — Managed realtime connections

Status: accepted — control-plane endpoint resources (2026-09-12)

## Decision

Gregale has an opt-in managed realtime data plane, separate from the existing
raw WebSocket/Upgrade bridge. `realtimed` terminates WebSockets, owns the
connection registry, sends heartbeats, enforces message/queue/lifetime limits,
and exposes connection operations over a DAC-protected Unix socket.

The reserved public path is:

```
/__gregale/realtime/{endpoint_id}
```

`gatewayd-internal` dispatches this namespace before hostname lookup, VM
admission, and the in-flight HTTP drain tracker. A quiet managed connection
therefore does not keep an application VM running. The raw bridge remains
unchanged and continues to be selected only for application-owned Upgrade
traffic.

## Endpoint and callback contract

An endpoint is registered on the local `realtimed` socket with:

```
POST /internal/endpoints
{
  "id": "notifications",
  "app_id": "...",
  "account_id": "...",
  "callback_url": "https://app.example.com",
  "connect_path": "/_gregale/realtime/connect",
  "message_path": "/_gregale/realtime/message",
  "disconnect_path": "/_gregale/realtime/disconnect",
  "callback_auth_token": "optional-callback-bearer",
  "auth_token": "optional-client-bootstrap"
}
```

Connect, message, and disconnect events are POSTed as JSON to the configured
ordinary HTTP handlers. `data` is JSON's base64 representation of the frame
bytes; `binary` preserves the WebSocket frame kind. Connect is accepted only
when the callback returns a 2xx response. Applications receive a stable
`connection_id` and can call the management API to send or close that
connection. Channel membership is explicit and publish is endpoint-scoped.

Management examples:

```
GET  /internal/connections
GET  /internal/stats
POST /internal/connections/{id}:send
POST /internal/connections/{id}:close
PUT  /internal/connections/{id}/subscriptions/{channel}
DELETE /internal/connections/{id}/subscriptions/{channel}
POST /internal/endpoints/{endpoint}/channels/{channel}:publish
```

The Unix socket is mode `0660` and owned by `faas:faas`; it is not a public
HTTP surface. Deployments must authorize endpoint registration and management
at the caller boundary. The bootstrap `auth_token` field is intended only for
controlled single-node deployments; production endpoint registration should
install an app-specific authorizer.

## Limits and operations

The daemon defaults to 10,000 concurrent connections, 1 MiB frames, a 64
message per-connection output queue, 30 second heartbeats, 10 second pong
wait, a 5 second write deadline, and a 24 hour maximum connection age. These
are configurable with `FAAS_REALTIME_*` environment variables. `/internal/stats`
exposes process-local accepted/rejected connection, message, byte, and queue
drop and callback-error counters for metering and alerting; aggregate across nodes because the
registry is intentionally node-local.

## Follow-up work

The socket owner, gateway routing, callbacks, durable endpoint/resource table,
authenticated `apid` CRUD API, and authenticated customer-facing
send/close/subscribe/publish operations are now shipped. The public operations
are endpoint-scoped and route through an owner interface; the current adapter
targets the local Unix socket and fails closed with `503` when no owner is
configured. The remaining production work is a leased cross-node registry or
deterministic node routing. That resolver should reuse the existing
`pkg/dispatch` retry/lease contracts rather than writing Postgres rows from
`realtimed`.
