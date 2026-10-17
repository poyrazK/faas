# ADR-1578: Internal CI execution archival and reusable history slots

- Status: implemented behind existing default-off internal gates
- Date: 2026-10-16
- Extends: ADR-1652, ADR-1654, ADR-1657 and ADR-1577

## Decision

Add an explicit, account-owned archive operation for terminal Jobs executions.
The existing 32 detailed-history slots/account become reusable after archival.
Allow a separate 128 compact archive receipts/account, each at most 16 KiB.
Archival is idempotent by execution UUID; response loss returns the same stored
receipt without another slot or audit event. No automatic archival worker,
deployment, provider call, public capability or feature-flag enablement is added.

All archive routes use the existing internal account allowlist, authenticated
admin scope, MFA, trusted HTTPS origin and no-store response boundary. Compute,
GitHub publication, transport and retention flags can remain off when cleanup
and retirement have already completed. Qualification executions cannot archive.

## Retirement and cleanup authority

Before compaction, require terminal execution state with finished time and no
execution lease. Every job in the persisted execution must be terminal.
Every CI-owned dispatched task must exist, remain attempt one, be terminal,
and either have no instance or have a stopped instance with retained terminal
output. Inconsistent intent/task links fail closed. Every retained runtime
grant must reference a stopped instance and have no live transfer lease.

All managed payloads for the execution must be provider-confirmed `deleted`,
with no live object operation. Ready, pending, deleting or lost-acknowledgement
retirement records prevent archival even after retention expiry. The existing
retention worker and provider retirement protocol remain authoritative.
Archiving never retires payloads or declares a VM stopped on its own.

When a queued task is cancelled before any VM exists, the Jobs adapter now
persists its terminal no-VM observation through the existing fenced output
receipt path. This lets staged source retire through the existing safety gate;
it does not weaken retirement checks or require a guest manifest that cannot
exist for a task which never started.

## Publication must settle first

Try-lock delivery bookkeeping without waiting for a publisher's network call.
A captured Check must be sent at exactly the terminal execution version, or
permanently ignored because authority was lost. Pending, sending, dead and
outdated sent delivery prevent archival. Every operator retry receipt must
already be applied. A lost remote response therefore remains recovery work
until the publisher reconciles it; archival cannot discard it.

An execution admitted without Check intent can archive once other gates pass.
No Check intent is created by archival. Existing Check IDs, acknowledged
versions/digests, retry generations, hashed retry identities and retry audit
rows remain intact. The archive receipt includes safe Check state and the
bounded public retry history. Archived rows leave the live delivery list and
publisher queue. An ignored Check may have no final remote result; its receipt
retains the ignored verdict and safe reason rather than claiming publication.

## Atomic compaction and preserved identities

Follow the existing storage reservation order: storage-budget lock, account
lock, execution row lock, then nonblocking delivery lock. Runtime/source writes
and late Jobs starts share account/execution authority; no network operation
runs inside this archival transaction. Per-account serialization also makes
archival and new history admission agree on quota accounting.

Insert the durable archive receipt, remove stopped runtime grants and runtime
object metadata, clear CI attempt source/output detail, and compact the
execution snapshot in one transaction. The parent row keeps its UUID, account,
revision, commit, idempotency key, terminal state, version and fence. The compact
snapshot is bounded to 1 KiB. All changes roll back together on receipt or late
compaction failure.

Keep CI attempt IDs, run mappings and fences. Ordinary Jobs replay and task
retry must remain blocked after output compaction. Ordinary Jobs run/task and
instance receipts remain under their existing ownership and lifecycle.
Managed-object retirement tombstones are not deleted or uncharged from their
permanent journal budget. Retain authenticated GitHub event snapshots and
event-to-execution links so redelivery cannot admit another execution.

Detailed reads/mutations and admission using an archived execution key return
`ci_execution_archived` (410). Current source/profile authorization can reject
admission earlier; the retained key still prevents another execution. The
separate archive APIs return safe receipt metadata: execution/revision IDs,
pipeline, commit, terminal state, definition digest, profile/automation identity,
times, job states/attempt IDs and Check/retry audit. No commands, step output,
logs, artifact paths, installation credentials or private image keys are copied.

## Bounds and operational limits

Accounts can retain up to 32 detailed Jobs executions plus 128 compact archives.
Archive receipts are permanent in this internal release. At the archive limit,
another archival attempt returns the standard quota error and leaves detail
untouched; repeated reads or idempotent archive calls consume no additional slot.
This is bounded internal history reuse, not unlimited execution retention.

The existing trigger/deduplication and storage-tombstone quotas remain separate
and can independently stop admission. Revision and profile quotas are also
unchanged. The Check retry allowance remains three generations/execution;
with archived identity retained it is bounded by 480 retry receipts/account
across 160 Jobs execution identities. Archive does not replenish retry budgets.

Account deletion cascades archive receipts with execution identity. Managed
storage tombstones continue to survive as designed in ADR-1652. Database
rollback refuses any archived data because removed detail cannot be restored.
Older binaries fail closed when decoding compact snapshots and cannot reuse
the retained idempotency keys.

## Verification

Portable PostgreSQL tests cover history-slot reuse, bounded archive receipts,
manual and webhook duplicate prevention, source retirement response loss,
native task/instance/transfer guards, publisher and operator-retry settlement,
concurrent archive acknowledgement recovery, account isolation/deletion,
rollback refusal and late transactional failure. API/CLI tests cover safe
receipts, stable errors and the authenticated HTTPS boundary. The full service
fixture retires queued-cancel source through a fake provider before archival.
No real VM lifecycle or provider qualification is performed in the cloud.
