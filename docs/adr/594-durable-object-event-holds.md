# ADR-594 · Manage exact-version event holds durably

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** ADR-584, ADR-592 and ADR-593

## Context

The native S3 adapter can observe and mutate variable retention, but owned
per-version mutations reject event holds. Fixed-policy equality cannot qualify
an event hold: ON returns a moving computed date and OFF fixes a date from the
provider's stored duration. Losing the release acknowledgment must not repeat
PUT or lose the remaining retention window.

## Decision

Extend the existing exact-version retention API, standard signed S3 subresource,
Go/Node/Python clients and CLI with event hold ON, duration changes and OFF.
Require the separate backend `object_lock.event_holds` enrollment and advertise
`version_event_hold`. Reads and recovery of accepted operations remain available
when enrollment or ingress is disabled. Deployment defaults remain disabled.

ON requires one positive days or years duration. OFF omits duration. OFF without
an explicit date requires a freshly observed ON policy with its computed date.
An explicit date is a minimum. Existing active fixed retention cannot be cleared
or shortened, and active COMPLIANCE cannot be downgraded. Duration changes over
an ON hold are allowed only with verified readback that preserves its observed
date. No governance bypass or legal-hold mutation is sent by retention changes.

Before dispatch, persist the native retention observation in a private immutable
`event_hold_baseline` under the current journal lease. This snapshot is separate
from customer intent and stable across retries. Memory and PostgreSQL stores
clone it, prohibit clearing/replacement, and require it before event dispatch or
ready settlement. A separate database guard survives replay of the old journal
migration. Old workers that omit evidence cannot dispatch or complete these
operations. SQL validates typed retention/duration shapes and rollback refuses
any event-hold receipts, including terminal retry identities. Source clone drain
and schema coverage include the new journal column.

ON readback must have the requested mode, ON status, equivalent configured
duration, and a date at least as late as the requested minimum and the baseline.
Years normalize to 365 days for equivalent duration comparison. OFF readback
must have the requested mode, OFF status and a final date preserving those
bounds. If the provider retains duration metadata after release, it must agree
with the baseline; omission is accepted. A worker does not invent a release
clock or exact final date. Malformed, missing or mismatched readback preserves
the dispatched fence and retries reads only. At most one native PUT is sent.
Known positive provider rejections remain terminal failures.

XML release intents and observations use distinct validation: OFF without a
final date is a valid conditional PUT but invalid provider readback. Existing
body, depth, field, duplicate-key and owned-version bounds remain in force.
Native calls are metered and retention changes do not change object capacity.
Current selectors remain explicit owned UUIDv4 or `null` in a freshly verified
permanently Enabled native Object Lock bucket.

## Limits and consequences

This amendment qualifies mutation on existing owned versions. Event-hold write
headers and new-version/default snapshots remain outside the fixed write
protection contract in ADR-592. Governance bypass and replication remain open.
The existing one-active-operation bucket fence, 45-second deadline, two-minute
lease, 30-second retry and bounded daemon batches are unchanged.

Out-of-band native policy changes can leave accepted work unresolved. A mismatch
after dispatch is not evidence that the PUT failed; operators must preserve the
journal rather than resetting dispatch to force another release.

## Validation

Local memory/PostgreSQL tests exercise exact targeting, UUID/null selectors,
ON/duration/OFF policies, captured policy bounds, lost acknowledgments, unknown
readback, reconstructed stores, disabled enrollment recovery, stable IDs,
request metering, stale leases, raw SQL guards and guarded migration replay.
Customer API → daemon → native HTTP and AWS SDK → TLS S3 gateway → native HTTP
cover release and single-dispatch readback recovery. Typed SDK and CLI tests
cover nested durations and conditional release requests. No live provider test
or deployment is required.

## References

- [Variable retention and retain-until-date behavior](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock.html)
- [Object Lock considerations and event hold request rules](https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lock-managing.html)
- [ObjectLockRetention](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ObjectLockRetention.html)
