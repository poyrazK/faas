# ADR 1091: Python native managed realtime WebSocket transport

Status: Accepted; runtime validation deferred.

## Context

The Python recovery coordinator still requires a caller-supplied session adapter.
Managed v2 sockets already provide ordered retained replay and processing ACKs,
while subscription ACKs only carry resume cursors.

## Decision

Export generator-preserved RealtimeWebSocketClient, RealtimeWebSocketSession and
RealtimeWebSocketMessage. Use optional websockets 15 via the realtime installation
extra, imported only on connection. Authenticate each fresh wss connection with
an async bearer-token provider, require the v2 subprotocol, disable redirects and
proxy auto-discovery, retain TLS verification and native Ping/Pong keepalive.

One session owns one channel or named principal inbox consumer and one reader
 task. Subscribe to server replay, then query read-state for authoritative history
bounds. Buffer retained events with a bounded queue and the server's 8 KiB frame
cap; overflow is explicit retryable failure, never silent event loss. Validate
stream identities, strict sequences and payload encoding/mutations. Yield replay
through the frozen target before live buffered events; acknowledge only when the
consumer advances after successful effects. Never advance user read markers.

For inboxes, replay server checkpoints behind local completed state; reject ones
ahead of application state. After successful snapshot restoration, reset the
durable inbox cursor before subscribing again. Midstream resync reconnects so the
coordinator can handle a fresh retention gap. Processing ACKs are not read receipts.

Authorization, policy/protocol, malformed messages and unknown failures stop;
explicit transient handshake statuses, socket loss, server history/checkpoint
failures and queue overflow reconnect. Credentials and raw error details are not
included in recovery errors. Closing cancels/joins the owned reader and closes the
socket. Coordinator cleanup provides cancellation shielding.

## Consequences

Applications need no custom WebSocket adapter for retained channels/inboxes, but
must supply authenticated tokens, event processing and optional snapshot loading.
Read-state support requires a verified principal. Ephemeral callbacks, filtering,
durable channel subscriptions and multiplexing remain outside this transport.
No server routes, schema or migrations change. Static lint, compilation, AST,
metadata and generator preservation/export checks run; runtime tests are deferred
at the user's request.
