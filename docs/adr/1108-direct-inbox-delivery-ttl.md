# 1108: Delivery TTL for direct realtime inbox messages

## Decision

Retained principal sends accept optional `ttl_seconds` (0–86400; zero disables
expiry). Positive TTL requires `delivery=retained`; live-only sends reject it.
Derive deadlines from original publication, not read time. Idempotent retries
compare delivery TTL alongside existing content identity and return the original
deadline. Inbox mutations inherit TTL and deadline without extending lifetime.

Inbox retention remains separate. Existing REST `expires_at` keeps its retention
meaning. REST/send responses expose `delivery_expires_at`; inbox history also
exposes `ttl_seconds` and `expired`. Expired history positions remain present with
empty payload, so consumers can advance without creating retention gaps.
Notification fallback/push TTL is independent; delivery expiry does not cancel
fallback jobs or shorten their configured lifetime. Publishers should explicitly
set notification_ttl_seconds when fallback lifetime also needs bounding.

Persist expiration rows in a sqlc-managed inbox companion table keyed to the
inbox message's sequence, with cascade deletion on normal pruning. Channel
expiration rows cannot reference inbox positions. Publication, deadline and
notification fallback state commit atomically. PostgreSQL history reads load
bounded expiration rows inside their read transaction. MemStore mirrors these
semantics and clones deadline pointers. No payload deletion or floor advancement
is triggered by delivery TTL.

Retained inbox WebSocket pumps send `inbox_checkpoint` with consumer, contiguous
sequence and code `message_expired`, without payload/message identity, for expired
positions. Checkpoints count toward the same unacknowledged window and can be
ACKed through the existing per-device durable cursor. Active messages expose the
optional absolute deadline. Inbox expiry selection cannot retract already queued
or in-flight frames. JSON-based private inbox gRPC already carries state message
expiry, requiring no protobuf change. SSE inbox subscriptions remain unsupported.

Node/Python SDKs validate subscription/consumer identity, reason and sequence before
advancing checkpoints. Expired positions never invoke normal message processors.
Expiry observers receive inbox identity and device name without OIDC principal or
payload. Local diagnostics add separate inbox counters. The process-local metric
`realtimed_expired_checkpoints_total` adds fixed transport label `inbox_websocket`.
Repeat observations count again; no exactly-once telemetry claim is added.

Owner event inspection accepts an optional principal and separate inbox cursor,
only after selected endpoint account/app ownership checks. It uses bounded history
reads and the existing expiry-aware projection, suppressing expired payloads and
showing original lifetime, deadline, and load-time status. Principal names are not
enumerated. Existing no-store/referrer protections and HTML escaping apply.

## Rollout and verification

Apply migration 20261012102700548 before upgraded apid readers/publishers. Upgrade
realtime nodes and checkpoint-capable SDKs before opting publishers into TTL.
Legacy inbox SDKs do not understand inbox_checkpoint. Existing sends remain
non-expiring by default. Downgrading/removing TTL data can expose old payloads;
disable TTL publishing and drain affected subscriptions before rollback.

Static Go builds, Node type checks, focused Python lint/compilation, whole-SDK AST,
OpenAPI/model contract checks and sqlc generation only. Runtime tests, schema
application and deployment remain deferred at the user's request.
