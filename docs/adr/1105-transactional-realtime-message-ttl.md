# 1105: TTL for transactional realtime messages

## Decision

Extend the explicitly installed customer realtime outbox with bounded immutable
per-event `ttl_seconds` (0–86400). New table installations default to the
transaction-local `gregale.realtime_ttl_seconds` setting or zero. Existing owners
apply the provided ALTER TABLE explicitly; no automatic customer DDL. The Node
SDK exposes a transaction-local default setter and optional per-event overrides.
Omission inherits, explicit zero disables expiry. Captured database changefeed
rows inherit the same transaction-local default. No transaction control is added.

Start the lifetime at retained publication, using the message creation timestamp
in the platform append transaction. The customer business commit timestamp is not
recorded by this outbox, so lifetime cannot start at that exact instant. Backlog
and relay retry delays do not consume message lifetime. This is per-event delivery
expiry, not atomic publication of all rows in one customer transaction.

Read leased events via sqlc with a JSON row projection of the optional column so
old customer schemas continue with zero TTL. Validate effective TTL through the
existing state append context. Expiration rows commit with message and receipt.
Include positive TTL in receipt content identity; omit zero to preserve prior
hashes. Receipts reconstruct expiry from original published_at and effective TTL,
even after history pruning or endpoint deletion. Changed TTL conflicts rather
than refreshing an accepted event. Existing checkpoints suppress expired retained
payloads without losing sequence continuity or reversing reducer/group effects.

## Rollout and verification

Retained expiry migration and checkpoint readers/SDKs precede opt-in publishers.
No additional platform migration is needed. Customer schema upgrades are manual.
Static Go builds, Node type checking, sqlc generation and whitespace checks only;
runtime tests and schema application remain deferred at the user's request.
