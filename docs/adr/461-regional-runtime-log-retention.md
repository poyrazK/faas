# ADR-461: Regional runtime log retention and failed-PUT orphan collection

Status: Accepted; internal rollout pending native qualification
Date: 2026-10-02
Extends ADR-434. Local backlog policy is superseded by ADR-462.

## Context

Regional archives have durable ownership and pending reservations, but object-store
lifecycle policies alone neither remove catalog receipts nor fence a delayed worker
that recreates an expired daily object. Local spool retention is seven days by default;
using that setting remotely would shorten Pro and Scale history. A failed PUT may
also have committed remotely without a publication descriptor.

## Decision

Apid owns a catalog-driven remote collector. Its retention window is the largest
`Plan.LogArchiveRetentionDaysMax()` in `pkg/api/limits.go` (currently 90 days).
The earliest retained UTC day is today minus that window plus one, matching the
inclusive gateway read boundary. Per-plan reads keep their existing shorter limits.
The local spool retention setting never changes this remote boundary. Unknown
plans, storage object timestamps, and physical worker regions do not grant ownership.

Each pass visits at most 256 descriptors on a five-minute cadence with a one-minute
context deadline. An `(archive_day, instance_id)` index supports stable pagination.
The cursor advances past failed routes and wraps at the end; an unavailable region
cannot indefinitely monopolize the first page. Multiple apids may collect concurrently.

Under the same app publication lock used by publishers and hard deletion, the
collector monotonically advances the retained binding's `expired_before` day before
remote DELETE. Reservations and publication refuse earlier days. A PostgreSQL trigger
also rejects direct inserts or updates that bypass the expiry check, and a separate
trigger forbids decreasing the floor. The binding and floor survive instance and
deployment garbage collection. Capturing the same instance again preserves the floor.

Only after a successful DELETE (or a confirmed missing object) does the collector
remove the daily descriptor and cascade its fragment receipts. Missing routes,
credentials, storage failures and metadata failures retain the descriptor for retry.
Reads refuse expired days even after the descriptor disappears; retained legacy
instances cannot bypass that fence through the uncataloged S3 fallback.

Pending reservations are eligible after the same remote boundary. They cover orphan
bytes from a PUT that timed out or succeeded before metadata publication. There is no
storage-wide heuristic deletion: an unknown object without verified ownership is
left for operator backfill or explicit removal. The exact canonical key and saved
app/account/region binding are required for collection. Cleanup does not fall back
across regions or to the global bucket.

Local unshipped fragments remain on disk when publication is refused by the remote
expiry fence; remote collection does not silently erase that backlog. A separate
catalog-aware local spool cleanup policy is still needed for permanently expired or
unattributed fragments. The existing local byte ceiling remains in effect.

## Operations and verification

Install the additive replay-safe migration before deploying the updated publishers,
reader and apid collector. Apid must have every participating archive route and any
legacy S3 credentials. Configure object-store policies to preserve at least the
longest plan window; the catalog collector is the authority for owned archive expiry.
The downgrade refuses to discard any non-default expiry fence.

The collector exposes `apid_log_archive_retention_scanned_total`,
`apid_log_archive_retention_collected_total`,
`apid_log_archive_retention_failures_total{operation}` and
`apid_log_archive_retention_last_success_timestamp_seconds`. Failure operations are
bounded to list, lock, retire, delete_object and delete_metadata. The success timestamp
covers one bounded pass, not a completed fleet-wide sweep. Monitor repeated failures
and cursor throughput against expired daily volume; the default batch/cadence is an
initial internal-rollout limit, not a claim of unlimited fleet throughput.

MemStore and PostgreSQL contract tests cover pagination, pending references,
monotonic expiry, preserved retry metadata, receipt pruning, instance GC, capture
replay and late publisher refusal. PostgreSQL tests also exercise direct SQL guards
and publication failure while holding an app lock with a two-connection pool.
Collector tests cover unavailable regions, delete/metadata retries, inclusive UTC
boundaries, unknown-object preservation and in-flight publisher ordering. Native
KVM archive publication/readback and lifecycle acceptance remain required by ADR-434;
portable tests do not qualify regional admission.
