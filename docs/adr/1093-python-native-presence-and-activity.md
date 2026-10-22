# ADR 1093: Native Python WebSocket presence and activity

Status: Accepted; runtime validation deferred.

## Context

Python already provides bounded presence directories, activity expiry and signal
publishers, but native dedicated/multiplexed transports ignore ephemeral frames.
Applications cannot use those trackers end to end without a transport adapter.

## Decision

Export generator-preserved RealtimeWebSocketPresence and RealtimeWebSocketSignal.
An optional binding per retained channel decodes server presence snapshot chunks,
joins, updates, departures and named signals into the existing presence directory
and activity tracker. Expose isolated combined summaries plus an optional generic
signal callback. Track snapshot readiness separately from retained replay readiness.
All callbacks are synchronous, fast and exception-isolated. Create directory timers
lazily on the owning event loop. Bound members and activity identities independently.

Support initial connection/principal presence scope and copied JSON state,
update_presence, send_signal, send_activity and clear_activity. Validate state at
512 UTF-8 bytes, signals at 2048 bytes, ASCII names at 64 characters and TTL at
0–30000 milliseconds. Serialize unescaped UTF-8 so outbound bytes match validation.
Server assigns member IDs and activity timestamps; publisher cannot assert identity.
Retain latest requested state for reconnect; do not replay transient signals.
Existing RealtimeActivityPublisher can wrap send_activity for refresh/coalescing.

Clear local members, snapshot readiness and activity timers on transport failure,
channel recovery failure or session close. Guard invalidation with the owning
session identity so an old session cannot clear replacement state. Rebuild only
from fresh frames after reconnect. Presence bindings cannot be shared between
active subscriptions or channels. Multiplexed unsubscribe disposes its binding;
dedicated callers dispose after stopping their recovery coordinator.

Allowlisted presence/signal rate-limit and relay rejection codes invoke an
optional safe callback without terminating retained replay. Publishing reports
local socket completion, not server acceptance; no automatic retry of ephemeral
updates. Unknown/malformed frames still follow the existing protocol failure path.
Include aggregate presence readiness/counts in per-channel diagnostics, excluding
member state and signal payloads. Inbox presence remains unsupported.

## Consequences

Native chat/collaboration clients can combine online membership and typing activity
without a custom socket adapter. Presence grouping is server-verified presentation
identity, not an authorization decision. Clocks must remain synchronized for the
existing bounded activity timestamp window. Signals are transient and may be lost
on reconnect; readiness does not imply global membership completeness. No routes,
server grants, schema or migrations change. Static lint, compilation, SDK AST and
generator export/preservation inspection run; runtime tests remain deferred at the
user's request.
