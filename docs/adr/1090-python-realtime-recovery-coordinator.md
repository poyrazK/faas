# ADR 1090: Python realtime reconnect and replay coordinator

Status: Accepted; runtime validation deferred.

## Context

Lifecycle observers and readiness gates expose recovery progress, but applications
still implement reconnect, replay and retention-gap orchestration themselves.
The Python SDK has no built-in managed WebSocket transport.

## Decision

Add a generator-preserved RealtimeRecoveryCoordinator over an explicit
RealtimeRecoverySession protocol, for one channel or inbox per run. Subscribe
from completed application progress, buffer live events before capturing an
authoritative history high-water target, scan ordered replay through that target,
then drain buffered/live events. Successful full-range replay permits advancing
across confirmed sequence holes. Reject unordered events; suppress completed
sequence duplicates. Adapter buffering must be bounded and overflow explicit.

A retention gap invokes an optional atomic application snapshot loader returning
a cursor within the captured history range; otherwise stop. Never automatically
retry processing/snapshot errors. Only explicit adapter RealtimeRecoveryRetry or
normal live stream termination reconnects. Use full-jitter exponential backoff
with bounded delays and a total retry budget per invocation. Configurable operation
timeouts bound connect, subscribe, replay, snapshot and cleanup; timeout stops.
Cancellation joins owned session cleanup; callbacks must cooperate. No parallel
application event handlers or new autonomous background loops are introduced.

Expose connection/subscription/catch-up observers and optionally record their
transitions in the incident timeline. Ready fires only after replay finishes;
transport-connected alone does not assert application freshness. Errors contain
safe reasons and suppress raw adapter/application exception chains. Cursor remains
local; the application owns durable transactional effect/cursor persistence and
its authenticated transport. No exactly-once guarantee or implicit read receipt.

## Consequences

Adapters must uphold the replay scan and buffering contract; subscribe ACKs cannot
stand in for authoritative targets. No server routes, schema or migrations change.
Static lint, compilation, SDK AST and generator export/preservation inspection
are performed. Runtime tests remain deferred at the user's request.
