-- filename: 20261026151950412_quiet_pause_ledger_ended_at_idx.sql
-- migration-compat: expand
-- ADR-20261023171905: meterd reads the quiet pauses overlapping one closed
-- minute (QuietPausedSeconds) every minute it rolls. Its filter bounds
-- started_at only from above, because a pause can last arbitrarily long, so
-- the started_at index matches the whole retained ledger (45 days) and the
-- planner scans the table. Pauses that overlap a recent minute either are
-- open or ended after the minute began: an ended_at index lets the planner
-- OR that range with the open-pause index, so each roll reads only the
-- pauses near the minute. The ledger is new and off by default; the index is
-- built concurrently so it never blocks schedd's writes.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'instance_quiet_pauses_ended_at' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX instance_quiet_pauses_ended_at';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS instance_quiet_pauses_ended_at
    ON instance_quiet_pauses (ended_at);

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS instance_quiet_pauses_ended_at;
