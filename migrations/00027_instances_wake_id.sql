-- +goose Up
-- +goose StatementBegin

-- Per-wake stable ID (spec §4 / observable-only follow-up to
-- gaps analysis 2026-07-23). The customer today sees only
-- x-faas-request-id, which is per-HTTP-request — every cold-wake
-- boundary mints a new one even for "the same wake". This column
-- is stamped by schedd at the Wake() Phase 2 INSERT (engine.go:254)
-- before vmmd is called; gatewayd propagates it back to the client
-- as x-faas-wake-id and the dashboard / CLI surface it.
--
-- Distinct from instances.id (the row PK, gen_random_uuid() at INSERT):
-- wake_id is per-wake-attempt, instances.id is per-row. A single
-- instance row can carry many wake_ids over its lifetime as the
-- app parks and wakes multiple times.
--
-- UUIDv7 (time-ordered) chosen over UUIDv4 for two reasons:
--   1. Time-ordered values cluster naturally, so the partial index
--      below lets the dashboard's "most recent wakes for this app"
--      list use an index-only scan, no separate sort.
--   2. The first 48 bits carry the unix-ms timestamp, which makes
--      log/dashboard scans human-friendly — operators can eyeball
--      "this wake is from ~12 minutes ago" without parsing.
--
-- The column default uses gen_random_uuid() (v4, available in PG
-- ≥13 via pgcrypto) as a safe fallback for any future code path
-- that INSERTs without explicitly providing wake_id. The engine's
-- Wake() call site overrides the default with uuid.NewV7() minted
-- Go-side so the platform-side guarantees time-ordered values; the
-- default is the safety net.

alter table instances
  add column if not exists wake_id uuid default gen_random_uuid();

-- Backfill every existing live row (any state — terminal rows
-- still get a wake_id for completeness; rows older than the
-- retention sweep are still in scope). gen_random_uuid() is
-- acceptable for legacy rows because the dashboard's "recent
-- wakes" scan only orders by recency of stamp and operators
-- never compare wake_ids across rows for ordering. Fresh wakes
-- minted Go-side are still UUIDv7.
--
-- Idempotent: the WHERE wake_id IS NULL guard means re-applying
-- the migration on a fresh DB is a no-op; on a partially-migrated
-- DB it backfills only the rows that haven't been touched.
update instances
   set wake_id = gen_random_uuid()
 where wake_id is null;

-- Enforce NOT NULL going forward. Same posture as 00024 / node_id:
-- the column-add was metadata-only on Postgres ≥11 (no default
-- rewrite because gen_random_uuid() is volatile), the UPDATE
-- above populated every row, so the constraint add is metadata-only
-- too. Not guarded by IF NOT EXISTS — Postgres has no such clause
-- for SET NOT NULL; the post-state is pinned by the test in
-- migrations/00027_instances_wake_id_test.go.
alter table instances alter column wake_id set not null;

-- Partial index supporting the dashboard's per-app recent-wakes
-- view. Rows in {parked, stopped, failed} are excluded — terminal
-- rows don't need wake-ordering (their wake_id is retained for
-- audit but never queried by recency). Matches the shape of the
-- existing watchdog partial index (00016).
create index if not exists instances_wake_id_app_idx
  on instances (app_id, wake_id)
  where state in ('waking','cold_booting','running','snapshotting');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverse-order teardown. Down-migrations on shipped platforms
-- require a manual runbook per the 00010/00013 posture, so this
-- Down section is the clean reverse; the loud-fail trust is the
-- operator's.
drop index if exists instances_wake_id_app_idx;
alter table instances alter column wake_id drop not null;
alter table instances drop column if exists wake_id;

-- +goose StatementEnd
