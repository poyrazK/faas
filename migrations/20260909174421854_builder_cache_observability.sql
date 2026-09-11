-- filename: 20260909174421854_builder_cache_observability.sql

-- +goose Up
-- +goose StatementBegin

-- Persist the cache decision made by builderd. The recipe digest is the
-- SHA-256 of the versioned BuildCacheRecipe, so it is safe to expose while
-- retaining the plan/runtime/base partitioning in the key derivation.
ALTER TABLE builds
  ADD COLUMN IF NOT EXISTS cache_status text,
  ADD COLUMN IF NOT EXISTS cache_key_sha256 text;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'builds_cache_status_check'
      AND conrelid = 'builds'::regclass
  ) THEN
    ALTER TABLE builds
      ADD CONSTRAINT builds_cache_status_check
      CHECK (cache_status IS NULL OR cache_status IN ('hit', 'miss', 'invalidated'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
    WHERE conname = 'builds_cache_key_sha256_check'
      AND conrelid = 'builds'::regclass
  ) THEN
    ALTER TABLE builds
      ADD CONSTRAINT builds_cache_key_sha256_check
      CHECK (cache_key_sha256 IS NULL OR cache_key_sha256 ~ '^[a-f0-9]{64}$');
  END IF;
END$$;

CREATE INDEX IF NOT EXISTS builds_cache_outcome_idx
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
