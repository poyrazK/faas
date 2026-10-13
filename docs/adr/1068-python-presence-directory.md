# ADR 1068: Python combined presence directory

Status: Accepted; runtime validation deferred.

## Context

The Python activity tracker expires named signals, but applications still assemble online membership and connect presence departures to activity cleanup. Node already offers a presence directory.

## Decision

Export immutable presence member/event/participant/summary models and a handwritten RealtimePresenceDirectory. The directory owns one activity tracker and offers apply, apply_activity, snapshot, reset and dispose. One synchronous nonblocking callback receives isolated combined snapshots. Use on a single running asyncio loop; transport and authentication remain application-owned.

Stage chunked snapshots until complete=True; replace visible membership only on completion. Apply live joined/updated/left changes to visible and pending membership. Validate incoming members and enforce capacity before committing maps. Bound total visible plus staged entries and channel count by max_members (1024 default, 1–8192). Bound activities independently. Presence state is a JSON object at most 512 UTF-8 bytes; connection count is a strict integer 1–512. IDs use verified server member IDs, not state labels.

Remove departed members' activity on leave or completed snapshot replacement, retaining ordering tombstones. Exclude unknown activity senders from combined summaries. Sort members by ID and activities by name; expose viewer and connection counts separately. Copy state/data on ingestion and observation. Suppress ordinary callback errors and close accidentally returned coroutines. Prevent recursive activity expiry callbacks during snapshot assembly.

Reset clears staged/visible presence and associated activity; dispose cancels expiry and rejects later ingestion. Presence membership follows server events, not a guessed local TTL; applications must reset on transport loss/reconnect. Preserve module and exports through generation.

## Consequences

Python UI/services can observe online and typing state together. Principal-scoped subscriptions group sessions while connection-scoped IDs remain distinct. Snapshots and callbacks are best-effort local views, not durable history. No routes, grants, schema or migration change. Static lint/compilation, AST/export/preservation checks and diff checks verify the implementation; runtime validation remains deferred by user request.
