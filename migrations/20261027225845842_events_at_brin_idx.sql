-- filename: 20261027225845842_events_at_brin_idx.sql
-- migration-compat: expand
-- Bug hunt 2026-10-27 O9: the daily events retention deletes rows older
-- than a cutoff, and none of the events table's 18 indexes leads with at,
-- so every run scanned the whole table. Events are appended in time order,
-- so a BRIN index on at is tiny and lets each retention batch read only the
-- oldest block ranges. Built concurrently so audit inserts never wait.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'events_at_brin_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX events_at_brin_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS events_at_brin_idx
    ON events USING brin (at);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS events_at_brin_idx;
