-- filename: 20261027220325644_snapshot_stale_at.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Stale snapshots are kept for api.SnapshotStaleRetention after they go stale
-- (Hunt 21 H21-2). Retention compared created_at, so a snapshot older than
-- the window that was marked stale today was deleted at the next sweep. A
-- trigger stamps the moment a row turns stale, whichever writer marks it, and
-- clears it if the row is made fresh again. Rows already stale keep NULL and
-- fall back to created_at.
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS stale_at timestamptz;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
         WHERE conname = 'snapshots_stale_at_check' AND conrelid = 'snapshots'::regclass
    ) THEN
        ALTER TABLE snapshots ADD CONSTRAINT snapshots_stale_at_check
            CHECK (stale_at IS NULL OR stale) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE snapshots VALIDATE CONSTRAINT snapshots_stale_at_check;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION snapshot_stale_at_stamp() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.stale AND NOT OLD.stale THEN
        NEW.stale_at := clock_timestamp();
    ELSIF NOT NEW.stale THEN
        NEW.stale_at := NULL;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS snapshot_stale_at_stamp ON snapshots;
CREATE TRIGGER snapshot_stale_at_stamp
    BEFORE UPDATE OF stale ON snapshots
    FOR EACH ROW EXECUTE FUNCTION snapshot_stale_at_stamp();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS snapshot_stale_at_stamp ON snapshots;
DROP FUNCTION IF EXISTS snapshot_stale_at_stamp();
ALTER TABLE snapshots DROP CONSTRAINT IF EXISTS snapshots_stale_at_check;
ALTER TABLE snapshots DROP COLUMN IF EXISTS stale_at;
