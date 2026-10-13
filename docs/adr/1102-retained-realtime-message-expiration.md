# 1102: Delivery expiration for retained realtime channel events

## Decision

Account-authenticated single retained-channel publishing accepts optional
`ttl_seconds`, zero/omitted for no expiry and 1–86400 for a bounded lifetime.
Both direct history append and channel `delivery=retained` publishing support it.
Live-only publishing rejects TTL. Batch, scheduled, publisher-credential and
transactional publishing interfaces do not accept TTL in this first version.

Persist delivery expiration in a companion row in the same channel-head-locked
transaction as the event. Store both the original TTL and the absolute deadline
calculated from the first event's committed creation timestamp. MemStore mirrors
this behavior. Identical idempotent requests require the same TTL and return the
original deadline, including after expiration. Editing/deleting an expiring
message inherits its deadline rather than extending its lifetime. Expiration
rows cascade away with ordinary retained-log pruning.

TTL is independent of retention. Do not delete expired sequence positions or
advance the channel retention floor for TTL. Keep expired rows until normal
retention/capacity pruning. Read pages retain authoritative bounds and sequences;
REST represents expired entries with `expired: true`, empty payload and suppressed
metadata. Retention usage/storage observations remain log-oriented, including
positions that replay as expiration checkpoints.

WebSocket v2 retained delivery emits `checkpoint` with `code: message_expired`
instead of an expired payload. Filtered and unfiltered subscriptions advance their
received position and can ACK the checkpoint using ordinary processing semantics.
SSE emits the same checkpoint with a sequence ID. Both live retained polling and
replay read the same authoritative log and check the server clock immediately
before selecting an event for output. Legacy live fan-out carries the deadline
and drops expired messages when dequeued for writing. Expiry cannot retract data
already selected/sent or reverse application state, reads, reducer effects or
consumer-group job processing; groups retain their separate delivery-deadline
policy. Clients should use expiry information for any desired local UI lifetime.

Propagate the optional deadline through private history gRPC and retained fan-out.
Add deadline observations to SDK message DTOs. Python recovery treats expiration
checkpoints like existing filtered checkpoints and skips application effects.
Node WebSocket consumers persist/ACK them without calling the message handler.
Node low-level SSE yields `checkpoint: true`; transport fallback persists that
position without invoking its application message callback. Manual SSE consumers
must branch on checkpoint entries before performing application effects.

Deploy the companion-table migration before updated apid/realtimed binaries,
then upgrade clients before opting publishers into TTL. Older unfiltered clients
may reject checkpoint frames, and old private history readers do not understand
the new expiry field. There is no automatic fleet downgrade guarantee. Rollback
removes TTL state, so retained events may become deliverable again; disable TTL
publishers and coordinate rollback rather than treating it as a data purge.

## Validation

Generated TTL SQL through pinned sqlc and regenerated the private history protobuf.
Go package builds, TypeScript type checking, Python lint/compilation/AST and OpenAPI
schema checks are static verification. Runtime expiry/replay/concurrency tests and
migration application remain deferred at the user's request.
