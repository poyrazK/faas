# ADR 1067: Python expiring realtime activity

Status: Accepted; runtime validation deferred.

## Context

Managed realtime already supports expiring presence, principal-scoped multi-session grouping and temporary named signals. Node applications have local activity tracking; Python consumers otherwise implement expiry and stale-packet handling themselves.

## Decision

Export handwritten RealtimeActivity and RealtimeActivityTracker, preserved by SDK generation and its barrel template. Consume decoded named signals from an application's existing authenticated transport; do not add another connection layer or server protocol. Construct/use on a single running asyncio loop. Key activity by channel/member/name, using server member IDs unchanged.

Enforce existing activity bounds: 30-second TTL, 2048 UTF-8 JSON bytes, names of 1–64 supported ASCII characters, channel 256 UTF-8 bytes and member ID 128 characters. Bound identity memory including tombstones to 1115 by default, configurable 1–8192. Reject capacity rather than evicting live state. Use exact normalized nanosecond RFC3339 revisions, ignore duplicate/stale updates and updates outside a 30-second clock window. Zero TTL or already-expired newer updates clear state. Keep ordering tombstones for the bounded delayed-packet window.

Own one asyncio timer for expiry/pruning; snapshot also prunes. Copy JSON data on ingestion and every snapshot/notification. A synchronous nonblocking on_change callback receives isolated snapshots; ordinary exceptions/callback cancellation are ignored and accidental coroutine objects closed. Presence departure removes member activity while advancing ordering markers. Reset forgets state/ordering on reconnect; dispose cancels timers and rejects future ingestion.

## Consequences

Principal-scoped subscriptions group verified users across sessions; connection-scoped IDs remain separate. Applications still own signal transport, subscription authorization, reconnect integration and clock synchronization. Ephemeral activity is best effort and not durable history. No server migration or permissions change. Static lint/compilation, SDK AST/export/preservation inspection and diff checks are used; runtime validation remains deferred by user request.
