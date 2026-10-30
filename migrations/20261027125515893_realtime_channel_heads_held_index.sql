-- migration-compat: expand
-- ADR-1323 (amendment of 2026-10-27): retained channel limits count the
-- channels that hold messages now, not every channel head ever created, so a
-- channel whose messages expired frees its slot. This partial index lets the
-- endpoint and account counts read only the heads that still hold messages;
-- dormant heads stay (their sequences must never restart) but are not
-- scanned. The previous release never uses the index.
--
-- The index is built concurrently (NO TRANSACTION), so
-- managed_realtime_channel_heads keeps taking writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS managed_realtime_channel_heads_held_idx
    ON managed_realtime_channel_heads (endpoint_id) WHERE oldest_sequence < next_sequence;

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('managed_realtime_channel_heads_held_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'managed_realtime_channel_heads_held_idx is invalid: DROP INDEX CONCURRENTLY managed_realtime_channel_heads_held_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS managed_realtime_channel_heads_held_idx;
RESET lock_timeout;
