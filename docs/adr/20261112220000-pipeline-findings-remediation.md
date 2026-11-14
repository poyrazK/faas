# ADR-20261112220000: Pipeline findings remediation

- **Status:** implemented and locally verified; production disabled
- **Date:** 2026-11-13 (Europe/Istanbul)
- **Amends:** ADR-1591, ADR-20261103230000, ADR-20261112202000

## Context

The two investigations in [the findings backlog](../pipeline-findings.md) recorded
sixteen issues involving credential readiness, running API refresh, pause and
monitoring, saved setup, CLI recovery, access revocation, queued verification,
and primary-key ordering. Their isolated reproduction probes remain historical
evidence; normal regression and native acceptance tests qualify the fixes.

## Decision

Prepare execution grants for the exact pinned worker-installed read hooks before
publishing a read-only credential, including issue, rotation and replay. Share
the SQL contract in `pkg/pipelineread`; validate function bodies, ownership,
signatures and search paths. Read-only validation permits only that bounded
definer helper, without granting access to private receipts. The worker also
serializes installation and credential grants with the same transaction advisory
lock, including the absence check, so neither side misses uncommitted work. It
repairs grants while paused, after completing native suspension and reader safety
actions. Source lookup/decryption is unnecessary for this maintenance.

On first read-hook installation or contract upgrade, request an ordinary durable
runtime restart for every bound API. Atomically stamp runtime configuration,
invalidate cached snapshots and enqueue the existing scheduler notification.
Retain a separate-purpose restart receipt so schema-workflow pruning cannot
repeat a delivered restart. Save a generation/version receipt remotely after the
catalog enqueue commits. Replication does not wait for deployment; the existing
runtime freshness boundary prevents stale snapshots from serving, and ordinary
app restart recovery handles delivery failure. No recurring per-VM SQL pool or
new service-to-service control path is added.

Health claims pause only after a fresh destination acknowledgement. Monitor
paused pipelines for stale or unavailable observations while avoiding false apply
alarms for intentional pause. Initial copy reuses durable native-copy high-water
marks and progress age; persistent lack of progress opens an existing
`sync_blocked` incident. Neither monitor starts destructive recovery.

Reuse a saved connection check only when it covers the saved table selection.
An unrelated search queues an exact check instead of blocking setup. Preserve
genuine unsupported/missing-table evidence and existing job/revision fencing.

Reader updates may revoke the final reader by applying an empty set; initial
creation still requires at least one subject. Preserve exact preview/approval
and private RLS receipts. Empty arrays deny all subjects while retaining rows and
replication, including during billing grace. Before terminal disconnect, apply
any approved reader reduction and acknowledge it before ordinary cleanup. Reject
disconnect while an approved change would grant new access until that change
finishes. This explicitly replaces the earlier one-reader minimum for updates.

Give a queued manual verification a stable request identity before remote work.
Permit revision-fenced cancellation during running or paused intent, including
before source access or creation of a physical proof. Match the manual attempt
when retiring a physical proof whose catalog acknowledgement was lost. Reject
new verification on paused/deleting intent. Upgrade legacy queued metadata without
starting work, and retain existing cancellation eligibility during grace.

Retain primary-key positions as transient planner metadata, outside approval and
schema hashes. Hydrate them when verification consumes durable table/repair plans.
Use the source's key order for new mirror indexes, scans, range bounds and
tracking. Include primary-index ordering in raw storage receipts, and actual query
ordering plus the replication-continuity epoch in proof fingerprints, to fence
DDL between metadata reads without changing raw receipts when planner metadata
is hydrated. Then
version the proof encoding, so older proofs restart safely without forcing a
schema resync. Preserve canonical collations, row encoding, budgets and locks.

CLI recovery waits for current observations before treating failures as terminal.
Use valid mirror names for digit-led slugs, stable digest suffixes for long source
names, and canonical UUIDs before lookup/request/idempotency construction. Resolve
optional Auth and API-name availability before resource mutation. Require typed
interactive confirmation or `--yes` for resync; automation and JSON output must
explicitly provide `--yes`. Keep disconnect's retained-row behavior separate.

## Compatibility and validation

The additive refresh-purpose column defaults to the existing schema purpose.
The reader constraint expands to 0–32 in separate add/validate/drop transactions;
validation scans without blocking writers. Legacy queued requests are backfilled
in batches of at most 1,000 rows, committing each batch. Rollback never invents a
reader or reverses a revocation. Deploy catalog changes, then all
pipeline workers, then API/dashboard/CLI surfaces that expose empty reader sets
and queued cancellation. The feature remains a staging pilot; no production or
provider qualification gate is relaxed.

Validation covers real RLS after final-reader revocation and disconnect, native
copy failures and recovery, late hook installation with signed HTTP reads,
provider credential issuance/rotation/replay, durable restart idempotency,
queued cancellation and stale workers, reverse/composite keys including INCLUDE,
CLI preflight/retry/confirmation, generated schema/SDK/docs and migration checks.
Executed commands, results and qualification limits are recorded in the
[remediation evidence](../ops/evidence/20261112-pipeline-remediation/README.md).
