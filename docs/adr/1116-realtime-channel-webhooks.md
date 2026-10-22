# ADR-1116: Transactional retained-channel webhooks

Status: explicit opt-in

## Context

Backend integrations need retained realtime events without maintaining a socket.
Existing app webhook infrastructure already provides signing, retries, inspection
and manual redelivery, but existing realtime webhooks describe inbox/schedule
outcomes rather than ordinary channel events.

## Decision

Add `realtime.channel.published` and one exact endpoint/channel binding per app
webhook, with optional existing bounded metadata key/value filters. Reuse webhook
quotas, MFA, app ownership, event selection and dispatcher. Bindings can target
only an endpoint owned by the webhook's account/app. Both an explicit event filter
and binding are required; empty catch-all event filters never receive channel
payloads. This avoids unexpected payload forwarding and event-volume increases
for existing subscriptions. No wildcard routing, independent worker or signing
format is added.

A platform PostgreSQL AFTER INSERT trigger captures recipients and a stable event
identity in the existing webhook event outbox within the retained append
transaction. This covers ordinary, transactional, scheduled and batch publishes,
channel edits/deletes and reducer expiry events. Rolled-back writes enqueue
nothing; idempotent replays that do not insert a new retained message enqueue
nothing. MemStore mirrors enqueue while holding its existing lock. Platform
trigger setup occurs through a migration, unlike owner-installed customer
changefeed triggers.

Configuration uses GET/PUT/DELETE on the app webhook's `realtime-channel`
subresource. Changing/removing a binding affects future capture only. Captured
recipients and payloads remain frozen; existing dispatcher rules govern queued
work. Clients must deduplicate by event_id and tolerate out-of-order delivery.

## Consequences

Webhook payloads contain retained message data encoded as base64 and metadata;
operators explicitly opt in and must review destination access. Sequence uses a
decimal string. Event identity survives webhook retry and does not depend on
retained history survival. No delivery acknowledgement or exactly-once processing
is promised. There is no backfill, live-only/ephemeral/inbox event capture, or
cross-channel ordering. Receiver republishing can create event loops; metadata
filters and receiver deduplication must prevent them. Edits/redaction cannot
recall already captured external payloads. Migration execution and dispatcher
qualification remain operator work, with runtime tests deferred in this session.
