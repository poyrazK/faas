-- filename: 20260909174421854_builder_cache_observability.sql

-- +goose Up
-- +goose StatementBegin

-- Persist the cache decision made by builderd. The recipe digest is the
-- SHA-256 of the versioned BuildCacheRecipe, so it is safe to expose while
-- retaining the plan/runtime/base partitioning in the key derivation.
ALTER TABLE builds
  ADD COLUMN cache_status text,
  ADD COLUMN cache_key_sha256 text;

ALTER TABLE builds
  ADD CONSTRAINT builds_cache_status_check
  CHECK (cache_status IS NULL OR cache_status IN ('hit', 'miss', 'invalidated')),
  ADD CONSTRAINT builds_cache_key_sha256_check
  CHECK (cache_key_sha256 IS NULL OR cache_key_sha256 ~ '^[a-f0-9]{64}$');

CREATE INDEX builds_cache_outcome_idx
  ON builds (deployment_id, finished_at DESC)
  WHERE cache_status IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS builds_cache_outcome_idx;
ALTER TABLE builds
  DROP CONSTRAINT IF EXISTS builds_cache_key_sha256_check,
  DROP CONSTRAINT IF EXISTS builds_cache_status_check,
  DROP COLUMN IF EXISTS cache_key_sha256,
  DROP COLUMN IF EXISTS cache_status;
-- +goose StatementEnd
