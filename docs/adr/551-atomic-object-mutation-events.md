# ADR-551: Atomic object mutation events

Status: Accepted (2026-10-03)

## Context

Tracked PUT, CopyObject, upload routes, multipart completion and deletion have
owned durable mutation journals. The event router has a durable fanout outbox,
acceptance-time subscription snapshots, claim recovery, recipient progress and
deterministic invocation IDs. Publishing after an HTTP response leaves a crash
window between the storage outcome and its notification.

Ordinary tracked write receipts also lack a persisted public version result:
the gateway previously mapped the native version after settling the write.
That cannot supply an exact version to an atomically published event.

## Decision

Publish a `gregale.storage` CloudEvent in the same transaction as each newly
confirmed tracked mutation. The existing `events` insertion trigger creates its
fanout receipt and captures enabled, owned subscriptions and work bindings.
MemStore mirrors that atomic boundary under its lock; it prepares version
references without committing them until publication succeeds.

New completions emit `object.created` for ordinary PUT, copy, upload routes and
multipart completion. Ordinary DELETE that creates a marker emits
`object.delete_marker.created`. Selected marker/version removal, unversioned
ordinary removal and lifecycle expiration emit `object.removed`. Data includes
the owned bucket, source app, key, operation, journal receipt, customer/lifecycle
cause and the public version selector when available. Create events also
include confirmed size and ETag. A marker-removal event identifies the removed
marker but is distinct from marker creation. An acknowledgment of an already
absent target is a completed removal intent, not proof that bytes were freed.

Event IDs are `write:<receipt>`, `multipart:<receipt>` and `delete:<receipt>`.
Terminal journal replay does not republish, including after the router's
30-day delivery identity retention. Each genuinely new mutation has a new
identity. No native version/upload IDs, backend placement, credential, subject,
request token or private lifecycle binding is included. Public version results
for tracked PUT/copy/route completion are persisted and resolved in the same
transaction; recovery supplies the exact native proof through that boundary.
The gateway returns this durable public result.

Pending, ambiguous, rejected, failed or expired preparations produce no success
event. Multipart part writes and aborts are not object creation/removal.
Legacy untracked completions and direct URL settlement have no authoritative
mutation proof and therefore do not produce events. Historical settled journals
are not backfilled. Mixed-version producers start publishing only when their
updated completion implementation is deployed.

Customers configure existing application event subscriptions under the existing
account-scoped deployment authorization. Bucket/key data filters, including
prefix/suffix conjunctions, select events; they are not an additional bucket
permission boundary. Subscriptions cannot receive another account's events.
Functions receive the canonical CloudEvent as an ordinary async POST invocation.
Subscription removal or replacement after acceptance does not redirect an
accepted event; a deleted target is terminally unavailable. Recipient progress,
retry backoff, stable invocation identity and operator failure/replay inspection
remain the existing event router's responsibility. Notifications are advisory;
the durable sweep recovers a lost wake or interrupted claim.

## Consequences

Each confirmed tracked mutation adds a ledger/outbox write. Publication failure
rolls back local completion, quota settlement and new version references;
provider acceptance remains recoverable through the existing mutation proof.
The mutation journal and accepted event are inseparable in PostgreSQL. Delivery
is at least once; customers must retain idempotency for their external effects.
There is no ordering promise across distinct mutations.

This decision implements the atomic event producer and function routing path.
S3 `?notification` configuration/`Records` payload compatibility, queue delivery,
and authoritative direct URL/provider-external mutations remain separate open
work. It does not claim those capabilities. Local HTTP/SDK, memory/PostgreSQL
reconstruction, rollback and delivery-failure tests qualify the implemented
boundary without using real providers.
