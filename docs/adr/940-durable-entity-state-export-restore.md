# ADR-940: Durable entity application-state export and restore

Status: local implementation; unqualified, internal engine API only.

## Context

Application mistakes can require recovering previous state. Replacing an internal
snapshot would also rewind receipts, outgoing work and ownership, potentially
repeating effects. Application data recovery must preserve current execution history.

## Decision

`Manager.ExportState` performs a read-only export of one authenticated immutable
committed snapshot. Format 1 includes the complete entity identity, source business
version, application JSON and a SHA-256 checksum over the compact JSON envelope
with an empty checksum. It excludes alarms, receipts, outbox and private authority.
Version-zero or missing entities cannot be exported. Concurrent cleanup can return
a retryable read conflict through the existing read path.

The checksum detects accidental corruption; it is not a signature or authorization.
Exports contain customer data and must not be logged or placed in public storage.

`Manager.RestoreState` requires an existing private ownership claim, a stable request
ID and a nonzero expected current business version. The source identity must match
the target exactly, including account, app, environment and tenant. Source format,
JSON and checksum are checked before execution. Restore cannot create an entity.
The operation fingerprint binds the export and expected version. Existing receipt
lookup precedes the expected-version check, so an acknowledged or uncertain restore
can be retried with the same request ID and payload. Reusing that ID with a changed
operation fails with the existing request-conflict error.

Restore uses the normal immutable upload plan, receipt journal, exact storage
accounting and final ownership/CAS checks. It advances the business version once
and replaces only application data. Current alarms, outbox, delivery reservations,
attempt counts and exhausted status survive; restore does not rearm failed work.
Existing receipts remain authoritative, including results produced before restore.
No guest code or external side effects run. Existing central size and storage
limits apply to the complete restore payload and resulting snapshot/journal.

Exports may contain application schemas incompatible with deployed handlers. This
engine deliberately treats application data as opaque; operators must select data
compatible with current schema-aware code. Restore is neither automatic downgrade
nor a complete disaster-recovery backup. It cannot reconstruct a lost/corrupt
receipt journal or clone state across scopes.

## Rollout and qualification

This slice adds trusted engine methods only. No public route, SDK endpoint,
automatic backup, deployment or production enablement is included. A customer
surface requires owner authorization, read/write scope gates, private no-store
responses, bounded decoding, mutation checks and acknowledged-write auditing using
the existing inspection/recovery patterns before exposure.

Source cases cover export, restore, receipt replay, stale expected versions,
identity/checksum rejection, stale ownership and preservation of exhausted alarm
and outbox work. Tests and builds have not been run in this workspace, per the
user's handoff. The testing agent must also qualify uncertain publication retries,
takeover during upload, projected-storage limits and concurrent cleanup/provider
behavior before calling the feature verified.
