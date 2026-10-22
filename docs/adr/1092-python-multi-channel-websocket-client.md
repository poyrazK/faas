# ADR 1092: Python multi-channel subscriptions on one WebSocket

Status: Accepted; runtime validation deferred.

## Context

The native Python v2 client opens a dedicated socket for each recovery coordinator.
Applications consuming several retained channels need fewer sockets while keeping
processing cursors, readiness and recovery isolated.

## Decision

Export generator-preserved RealtimeMultiChannelClient and
RealtimeChannelSubscription. Share the existing authenticated native handshake,
TLS/subprotocol validation, heartbeat and frame bounds. One lazy connection pool
and socket reader route directly to separate bounded channel queues. Admit at
most eight registered channels, matching the existing managed v2 connection limit.
No server quota changes. Dynamic subscribe starts an owned per-channel recovery
task and returns a handle; duplicate channels are rejected before network work.

Reuse the existing recovery coordinator for each channel's processing cursor,
ordered replay, authoritative target, snapshot loader and bounded retry policy.
Slow/failing processors, retention gaps and buffer overflow affect their channel.
Connection loss affects every attached session; reconnect admission is serialized
so channels share one replacement socket. Unclassified connection-level frames
fail all sessions. No application effects or snapshot failures retry automatically.

Expose per-channel readiness waits, cursor, safe terminal error, observation
trackers and bounded incident timeline/redacted diagnostics. Readiness is tagged
with the current local attempt and catch-up generation and set only after replay
finishes, not merely when transport connects or progress reaches the target.
Cancel a wait without cancelling recovery. Failed handles remain registered until
explicit removal, preserving inspection and bounded admission.

Unsubscribe cancels/joins cooperative processing, waits for wire removal ACK or
not_subscribed before reusing the channel identity, and returns completed progress.
Keep retiring registrations until that barrier and ignore their trailing frames.
Uncertain removal closes the shared socket; other channels recover from their
own completed positions. Closing the last session drops the socket. Client close
rejects new admission and joins owned recovery/cleanup/reader tasks with repeated
cancellation shielding. Ignore failure callbacks from obsolete attempts.

## Consequences

Up to eight channels consume one socket with independent tasks, queues and replay
readiness. Shared network failure still affects all streams. Processors must
cooperate with cancellation; application effects/cursor persistence remain caller
owned, without an exactly-once guarantee. The initial multiplexed API covers
retained channels; inboxes retain their existing dedicated client.
No server routes, schema or migration changes. Static lint, compilation, SDK AST
and generator export/preservation checks run. Runtime tests remain deferred at the
user's request.
