-- filename: 20261106103734657_managed_postgres_branch_expiry.sql
-- migration-compat: expand

-- ADR-20261106103654: a restore (a branch) may carry a TTL. The copy records
-- when it ends, and the lifecycle reconciler purges it then. An older apid
-- ignores the column: it never expires a copy.
--
-- The column is nullable with no default, so adding it writes no rows. The
-- partial index serves the expiry sweep and is built concurrently (NO
-- TRANSACTION), so managed_postgres_databases keeps taking writes.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- The table holds one row per customer database (hundreds), so the build is
-- short; the migrator's 30 s statement_timeout must still not cancel it
-- halfway and leave an invalid index.
SET statement_timeout = 0;

ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS expires_at timestamptz;

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'managed_postgres_databases_expires_at_idx' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX managed_postgres_databases_expires_at_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS managed_postgres_databases_expires_at_idx
    ON managed_postgres_databases (expires_at, id) WHERE expires_at IS NOT NULL;

RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DROP INDEX CONCURRENTLY IF EXISTS managed_postgres_databases_expires_at_idx;
ALTER TABLE managed_postgres_databases DROP COLUMN IF EXISTS expires_at;

RESET lock_timeout;
