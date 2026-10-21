# ADR-2026101701: Reviewed retention of application repair payloads

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Scope: owner-installed PostgreSQL 16 capture journals and plan row values
- Extends: [selective repair](20261016-selective-data-repair.md)
- Superseded-parent eligibility is extended by [ADR-2026101704](2026101704-repair-superseded-retention.md); unresolved and closure-only plans retain their holds.

## Problem

Captured keys and before/after values remain sensitive after an incident ends.
Deleting whole operations or plans would also erase the identities and receipts
needed to reconcile a lost COMMIT response and prevent another correction with
the same identity. Expiry does not establish the outcome of an approved repair.

## Decision

Provide an explicit maintenance CLI with a saved inventory, exact digest,
operator identity and confirmation. There is no automatic purge worker or hosted
purge endpoint. The database owner installs the SQL; application capture writers
and executor bearer credentials acquire no maintenance authority.

The default cutoff is database time minus seven days. Owners can choose an
older cutoff for a longer retention policy, but cannot shorten the minimum.
Eligible payloads are:

| Payload | Required evidence |
|---|---|
| Plan row items | Plan creation and apply receipt are older than the cutoff, or the plan was never approved and its expiry is older than the cutoff. |
| Operation changes | Finalized operation created before the cutoff, with no remaining uncompacted plan referencing it as source or repair operation. |
| Approved plan with no apply receipt | Always held, including after expiry. |

Plan compaction happens before its source and inverse journals become eligible
for a later inventory. A new plan pins its source. Rechecking the entire saved
inventory rejects a batch if any entry changed. It never substitutes another
eligible record or drops a conflict from the approved set.

Each inventory has at most 32 entries and 64 MiB of estimated logical payload,
expires after 15 minutes, and contains only identity, state hashes, counts, sizes
and closed eligibility reasons. Its metadata is bounded to 1 MiB. Applied
inventories retain the original actor label, database session identity and time;
same-ID/digest replay returns that receipt even after expiry. A lost purge COMMIT
response requires inspecting this saved identity before any retry.

All bounds live in `pkg/api/limits.go`. Purge uses a ten-second transaction
budget and two-second lock budget. The engine explicitly selects READ COMMITTED;
the SQL refuses other isolation levels. Application capture, plan preparation,
evidence, approval and apply hold a shared scope advisory lock. Purge holds the
exclusive counterpart through revalidation and commit. Source selection also
locks the operation row, preventing an older repeatable-read snapshot from
reconstructing a plan after purge. Unrecorded ordinary writes need no new lock.

## Permanent evidence

Purge deletes `gregale_repair.changes` for selected operations and replaces
selected plans' `document.items` with an empty array. It preserves:

- Source and repair operation IDs, scope, release, enrollment, schema fingerprint,
  transaction identity, finalization and repair linkage.
- Original plan ID/digest, summary counts, check hash, approval, rehearsal evidence
  and apply receipt, with a payload-purged timestamp.
- Retention inventory IDs/digests and their purge receipts.
- Business rows, enrollment, inserted-row identities and monotonic row versions.

The compacted plan has a separate retained-document digest. Purge first verifies
the original document digest, and apply verifies the appropriate full or retained
digest before returning a receipt. This prevents compaction from accepting
corrupted metadata as a new baseline. The original plan digest continues to
identify the approved correction. Repeating apply with its original repair ID
returns the durable receipt without rechecking or executing the inverse writes.
A different repair ID fails. A new plan from a purged source returns the stable
`repair_payload_purged` error; existing plan IDs remain inspectable as metadata.

The installed protection triggers permit only the owner-executed maintenance
marker transition and scoped change deletion. A transaction marker alone does
not authorize a writer to bypass journal immutability. Maintenance functions use
SECURITY DEFINER, a fixed `pg_catalog` search path and qualified table references.
PUBLIC execution is revoked. A dedicated maintenance role needs only schema
usage and execute grants for the three maintenance entry points.

Fresh installation and the explicit upgrade share one canonical function
contract. The upgrade preserves enrollment, fingerprints, approval and existing
receipts. It does not reenroll targets or run from a constructor or request.

## Boundaries

This is logical application-payload deletion. MVCC, WAL, backups, exported dumps
and already delivered responses have independent lifecycles. It makes no secure
erasure claim. Small identity and audit tombstones remain indefinitely in this
version; there is no automatic identity recycling.

Native launch/producer/task ownership journals and active control-plane recovery
records are outside this purge. Cleanup and reservation release still require
the original retirement proof. An expired unapproved application plan may lose
its values while native cleanup remains pending: cleanup consumes the original
ownership frames, not the source values. Approved uncertain applies remain
pinned. This feature provides no new native cleanup or replay authority and does
not qualify native execution for rollout.

## Validation

Private PostgreSQL tests cover the 500/40 correction, compacted receipt replay,
young and approved-uncertain holds, entry/byte limits, whole-batch invalidation, lock contention,
stale snapshots, scope boundaries, privilege separation, corruption rejection,
expired inventory replay and repeatable owner upgrades. The strict repair gate
also exercises the actual CLI through authenticated rehearsal/apply, plan and
journal purge, and post-purge receipt replay with all business results preserved.
