-- migration-compat: expand
-- ADR-1155 (amendment of 2026-11-02): profile captures count their hourly
-- quotas, storage and running captures in a pool (an organization, or an
-- account), read as coalesce(capture->>'pool', account_id::text). Every
-- capture queued and every claim filters on that expression, so it gets its
-- own index; the previous release filters by account_id and never uses it.
--
-- The index is built concurrently (NO TRANSACTION), so profile_captures
-- keeps taking writes. Re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

CREATE INDEX CONCURRENTLY IF NOT EXISTS profile_captures_pool_created_idx
    ON profile_captures ((coalesce(capture->>'pool', account_id::text)), created_at DESC);

-- A failed concurrent build can leave an invalid index. Do not record success
-- when IF NOT EXISTS skips one; require explicit cleanup before retrying.
-- +goose StatementBegin
DO $index$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('profile_captures_pool_created_idx')
                 AND indisvalid AND indisready) THEN
    RAISE EXCEPTION 'profile_captures_pool_created_idx is invalid: DROP INDEX CONCURRENTLY profile_captures_pool_created_idx and retry the migration';
  END IF;
END
$index$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS profile_captures_pool_created_idx;
RESET lock_timeout;
