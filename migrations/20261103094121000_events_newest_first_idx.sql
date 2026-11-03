-- filename: 20261103094121000_events_newest_first_idx.sql
-- migration-compat: expand
-- ADR-20261103094121: the operator events list (GET /v1/admin/obs/events,
-- ListAllEventsPaged) reads events newest first. events had no b-tree on
-- at (only events_at_brin_idx, which cannot return rows in order), so a read
-- without a narrow filter scanned and sorted the whole table and hit the
-- statement timeout. A backward scan of (at DESC, id DESC) returns the newest
-- rows first under any combination of the list's optional filters.
--
-- Built concurrently (NO TRANSACTION), so events keeps taking writes. The
-- previous release's query benefits as is. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS events_at_id_idx
    ON events (at DESC, id DESC);

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('events_at_id_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'events_at_id_idx is invalid: DROP INDEX CONCURRENTLY events_at_id_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS events_at_id_idx;
RESET lock_timeout;
