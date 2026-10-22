# ADR 1097: Python native live-only direct messages

Status: Accepted; runtime validation deferred.

## Context

Managed realtime already routes direct messages to verified connected principals
and optionally confirms processing receipts. Native Python clients reject these
socket-wide frames and offer only retained channels/inboxes plus ephemeral signals.

## Decision

Export generator-preserved immutable RealtimeDirectMessage and reusable
RealtimeWebSocketDirectMessages. Add an optional socket-level handler binding to
native transport configuration and enable direct_messages on channel/inbox subscribe.
Dedicated sessions own their binding; the multiplexed connection pool owns one
binding for the shared socket. Route direct/control frames before channel routing,
so a message is handled once per socket, independent of channel registrations.

Strictly validate optional IDs, required receipt IDs, boolean flags and base64,
with 4096-byte payload bound. Use one separate async serial worker and bounded queue
(default 128, maximum 8192) so direct processing does not block retained frame
routing or ACK confirmation. Send requested receipts only after successful handler
completion. Match acknowledgement responses by ID, retain a bounded recent-ID set
for duplicate retry responses, and retry only receipt writes (default two retries,
five-second attempt deadlines). Exhaustion reports a safe error without advancing
any retained/read/durable cursor or replaying application effects.

Handler failure reports a sanitized fatal connection error; no automatic handler
retry or failed-message ACK. Queue overflow and transport failures reconnect via
existing recovery, with explicit errors. Connection loss cancels/discards live work
without replay; close invalidates and joins only tasks belonging to that socket.
Bindings can be reused across reconnects, but never concurrent physical sockets;
explicit asynchronous close disposes the binding after client shutdown. Error
callbacks are synchronous, fast and exception-isolated. Payloads stay out of reprs
and diagnostics; unknown/malformed frames retain protocol failure behavior.

## Consequences

Backend targeted UI commands/session warnings need no custom Python frame adapter.
Direct receipts confirm processing, not human visibility. Live delivery is not
restart durable; retained inboxes remain the delivery-survival mechanism. Handlers
must cooperate with cancellation and application effects remain caller-owned.
Direct-only sockets still require a channel/inbox subscription to opt in. No server
routes, grants, schema or migrations change. Static lint, compilation, SDK AST and
generator export/preservation checks run; runtime tests remain deferred by request.
