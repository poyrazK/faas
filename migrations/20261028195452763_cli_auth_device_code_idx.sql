-- filename: 20261028195452763_cli_auth_device_code_idx.sql
-- migration-compat: expand
-- Bug hunt 2026-10-28 H67: the CLI's exchange looks its code up by device
-- hash. Built concurrently so sign-ins never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'cli_auth_codes_device_hash' AND NOT i.indisvalid) THEN
  -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
  -- would keep; drop it so a rerun builds it again.
  EXECUTE 'DROP INDEX cli_auth_codes_device_hash';
 END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS cli_auth_codes_device_hash
    ON cli_auth_codes (device_hash) WHERE device_hash IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS cli_auth_codes_device_hash;
