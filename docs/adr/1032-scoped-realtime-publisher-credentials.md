# ADR 1032: Channel-scoped realtime publisher credentials

Status: accepted for preview implementation; migration/runtime validation pending.

## Decision

Issue opaque 256-bit `grp_` credentials with exact endpoint, channel and event-type
grants. Store only SHA-256 token hashes. Account administration and MFA control
issuance, rotation and revocation; metadata listing follows app read access.
Producer-only routes accept these credentials without account credentials. Enforce
current account/app status, enabled endpoints and realtime plan eligibility.

Every publish requires an idempotency key and commits through the retained-channel
transaction. Authorization is checked again under database locks; schema checks,
reducers and channel webhooks retain their existing behavior. Store receipt,
accepted usage and rate accounting atomically with the event. Matching retries
return the original sequence without another event or publish quota charge;
conflicting content returns 409. Event types are explicit labels recorded in the
reserved `event_type` metadata entry, independently of payload schema validation.

Limit each endpoint to 64 credential records, each key to 32 exact channels and
16 event types, and payloads to at most 4096 bytes (or the smaller endpoint/key
limit). Default expiry is one day, maximum 30 days. Default accepted publish rate
is 60 per fixed clock minute, maximum 600; boundary bursts are possible. Retain
up to 1115 receipts per key for 24 hours. This is a bounded preview admission
limit, so sustained publishers may exhaust receipts before their minute quota.

Rotation preserves scopes, identity, receipts and rate counters. Revocation stops
future transactions after its lock commits; an already authorized transaction
may finish first. Issuance cleans expired/revoked metadata older than seven days.
Receipts survive channel history trimming, but not endpoint/credential deletion.
After receipt expiry, a key no longer guarantees deduplication; retained frames
with the same stable publish identity can still cause a conflict.

Realtime fanout is best effort after durable commit. A partial response still
means the event is retained; history recovery supplies missing frames. This does
not guarantee client receipt or exactly-once application effects.

Expose payload-free usage and lifecycle metadata in the inspector. Provide an
isolated Node publisher client with per-request credential providers and generated
Node/Python APIs. Do not inherit global account authentication or cookies.

## Deployment and validation

Apply migration `20261012102700476_realtime_publisher_credentials.sql` before
activation. Go and Node builds and static specification checks are the current
validation scope. Runtime, migration and UI checks remain deferred by request.
