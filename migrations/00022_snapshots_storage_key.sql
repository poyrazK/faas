-- +goose Up
-- +goose StatementBegin

-- Issue #96 / ADR-025 axis 2 (final slice): the canonical
-- StorageBackend key for each snapshot blob. Slice 1 (PR #106) put the
-- StorageBackend interface in place; slice 2 (PRs #108 + #109) made
-- OCIRegistryStorageBackend honor it. What was missing was the DB
-- column that carries the key — schedd's wake path today still
-- computes the legacy local path via sched.snapshotPaths().
--
-- storage_key is the durable carrier: it names the artifact in the
-- storage backend regardless of whether the wired backend is local
-- ("snap/<deployment_id>/mem") or remote (e.g. an OCI registry under
-- that exact tag). The legacy `path` column is kept for one milestone
-- (deprecation window per #96 follow-up) so old wakes that read it
-- still work; new writes populate BOTH and the read path prefers
-- storage_key. The next slice drops `path` and the `MemPath` proto
-- field together.
--
-- The backfill is a literal SQL concat that mirrors sched.SnapshotMemKey:
--   "snap/" || deployment_id::text || "/mem"
-- This means backfilled rows agree byte-for-byte with what
-- sched.SnapshotMemKey(<dep>) returns, so the read-path can switch
-- from path to storage_key with no migration-time double-read.
--
-- Idempotent (IF NOT EXISTS) so the migration can be re-run during
-- local development without manual cleanup.

alter table snapshots
  add column if not exists storage_key text not null default '';

-- Backfill every existing row. snapshot_id is non-nullable, every
-- deployment row has a valid UUID (FK enforces it), so the concat
-- is total. Empty-string rows only exist post-insert and have no
-- deployment to compute from — the default already handles them.
--
-- The `created_at < now() - interval '1 second'` fence is a
-- rerun-safety belt: if a re-applied migration interleaves with
-- an in-flight imaged insert (e.g. a developer re-runs the
-- migration during a local repro), the WHERE skips any row that
-- was stamped in the last second. The 1s slack is harmless on a
-- cold-boot path (snapshots are seconds old by the time anyone
-- queries them) and rules out the overwrite race entirely.
update snapshots
   set storage_key = 'snap/' || deployment_id::text || '/mem'
 where storage_key = ''
   and created_at < now() - interval '1 second';

-- (No new index needed: storage_key is read together with deployment_id
-- and the existing snapshots_deployment_idx covers the wake lookup.
-- A future GC-by-storage-key query would want one; defer until that
-- query exists.)

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
alter table snapshots drop column if exists storage_key;
-- +goose StatementEnd
