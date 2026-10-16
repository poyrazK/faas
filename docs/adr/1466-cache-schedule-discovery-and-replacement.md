# ADR-1466: Cache schedule discovery and atomic replacement

Status: Accepted

## Context

One-time cache schedules can be inspected or cancelled by UUID, but agents need
bounded discovery after reconnecting and reviewed changes to maintenance times.
Editing schedule time in place risks stale reviews, moving pagination keys and
ambiguous worker-claim races. Cache intent remains owned by apid.

## Decision

Expose read-scoped named-environment discovery ordered by immutable execution
timestamp and UUID. Status and inclusive-from/exclusive-until filters bind the
opaque base64 continuation alongside immutable app/project/environment IDs.
Default page size is 20 and maximum 50, centralized in api/limits.go. Read limit+1
rows from one repeatable-read snapshot; pages remain live across requests. New
records or state transitions can require restarting. No unscoped account listing,
raw gateway identities or implied convergence is introduced.

Rescheduling is an immutable replacement: preserve action body and original
policy expected revision, generate a fresh schedule/action UUID and execution
time, cancel the original and insert a successor in one transaction. Self links
are rejected, predecessor/successor links are unique and deferred foreign keys
allow both links to commit together. The original remains a cancelled receipt;
new records expose replaces_schedule_id and old ones replaced_by_schedule_id.
This extends migration 20261016153051002 through a new append-only migration.

Lock account, app and environment in existing intent order, then lock the exact
original schedule row before inspecting pending status. Worker SKIP LOCKED claims
serialize with replacement; only one may win. Concurrent cancellation or competing
replacement rejects the new write. Timestamp guards and immutable UUID pin review
identity. Replacement consumes the same active quota slot; UUID conflicts roll
back the entire transaction. Recover a matching successor before time/revision
admission so reply-loss retries remain safe after terminal state or later edits.
Policy guard changes reject a new replacement rather than silently rebasing it.

MCP planning is read-only, 15-minute and bound to exact target/action/times. Apply
requires current named-environment write consent; encrypted journals retain both
attempt and receipt. Cached replies still recheck consent. Native APIs enforce
owner eligibility, target identity and retry invariants independently of MCP.

## Consequences

Replacement chains preserve history without changing existing sort keys. Cancel
the successor UUID to withdraw the new delegation; cancelling its cancelled
predecessor is an idempotent acknowledgement, not recursive cancellation. Accepted
replacement is separate from action execution and gateway convergence. Retained
receipts and chains grow until owner deletion; pages are not cross-request
snapshots. Rollout requires migration 20261016153051010 then updated apid.
