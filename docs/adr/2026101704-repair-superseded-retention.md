# ADR-2026101704: Reviewed compaction of superseded repair plans

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [repair retention](2026101701-repair-retention.md), [linked replacements](2026101702-repair-replacement.md) and [apply resolution](2026101703-repair-apply-resolution.md)
- Scope: owner-installed PostgreSQL retention functions, Go maintenance API, CLI and portable acceptance. No control-plane schema or VM lifecycle change.

## Decision

Allow the existing reviewed retention workflow to compact the row payload of a
superseded parent with a verified saved replacement. A committed replacement
is a source fence: replacement and apply take the same parent row lock through
commit, and insertion of the child and the parent pointer are atomic. A parent
with a committed apply cannot enter replacement. Explicit apply closure is
preserved when present, but is not required for a directly replaced parent.

The parent's creation and the replacement's creation must precede the inventory
cutoff, which cannot be newer than database time minus seven days. Parent age,
approval age or expiry alone cannot make a fresh replacement eligible. Closing
an apply without saving a replacement keeps the parent's values held, including
for never-approved parents. Approved, unresolved plans remain held after expiry.

Metadata-only classification bounds the candidate set before hashing documents.
For a selected superseded parent, verify its original document hash, the child's
full or retained document hash, matching scope/source/enrollment/schema/table,
parent ID/digest, and bounded, conserved candidate/exclusion comparison. A bad
selected hash or lineage rejects the inventory instead of creating a new trusted
baseline. Missing or foreign children remain held. No captured keys or field
values enter the retention inventory.

Each eligible parent entry binds its replacement ID, original digest and
creation time as `superseded_at`, in addition to the parent's complete record
hash. Child approval, receipt or legitimate child compaction does not invalidate
that immutable fence. The existing exclusive scope lock and READ COMMITTED
whole-batch revalidation run before any compaction. A changed fence, parent or
invalid child rejects the entire transaction. Replacement, resolution, capture,
evidence, approval and apply retain their shared scope lock through commit.

The inventory adds fixed `holds` groups with kind, reason and count. They
distinguish unresolved applies, closure awaiting a replacement, recent or
unverifiable replacements, receipt/unapproved age, and journals required by a
plan. The existing `held_approved_plans` field counts approved, unapplied held
plans, excluding eligible superseded parents. Hold groups are review context;
the frozen entries and digest remain the only purge authorization. Old saved
inventories remain readable and replayable without the new metadata.

## Evidence and ownership

Compaction still empties only `document.items` and saves a retained-document
hash and purge timestamp. Preserve original identity/digest, summary counts,
approval, rehearsal and check history, apply closure, replacement pointer and
child lineage/comparison. Same-ID closure/replacement recovery and committed
receipt replay continue after compaction; parent approval and delayed applies
remain fenced. There is no expiry refresh, approval transfer or identity reuse.

Every uncompacted plan continues to pin its source and inverse journals. An
active replacement therefore protects the original capture after parent
compaction. Source journal removal requires another reviewed batch once all
referencing plan payloads have been compacted. Native ownership evidence and
control-plane recovery reservations remain outside application retention.

Fresh schema and the repeatable, explicit owner upgrade use the same functions.
No startup DDL, automatic purge, hosted purge endpoint or additional maintenance
privileges are introduced. Existing inventory, byte, timeout and retention
bounds remain centrally defined. Logical payload deletion retains the existing
MVCC, WAL, backup and export boundaries.

## Verification

PostgreSQL fixtures and the real CLI journey exercise failed apply, durable
closure, saved 459/41 replacement, reviewed parent compaction, recovery of both
identities and fresh replacement rehearsal/approval/application. Private fixture
metadata is backdated to exercise the seven-day boundary without waiting; the
replacement's fixed expiry is preserved and production has no age bypass.
Direct and scheduled hosted journeys compact the parent while a lost replacement
response is still reserved, then recover closure and admit the same child.
Checks also cover closure-only holds, fresh replacement holds for approved and
unapproved parents, direct replacement, changed/corrupt child evidence,
scope boundaries, delayed parent applies, linked-chain compaction and owner
upgrades preserving history. Existing privilege and atomic retention checks
continue to apply.
