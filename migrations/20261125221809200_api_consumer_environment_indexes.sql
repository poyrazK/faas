-- filename: 20261125221809200_api_consumer_environment_indexes.sql
-- migration-compat: expand
-- ADR-20261104120000 amendment 2026-11-22: a key prefix, a key name and a
-- consumer's external reference are unique within one environment of an app,
-- so a stage can hold a copy of a production consumer and key. Production is
-- the zero UUID in the identity. Built concurrently so consumer and key
-- writes never wait on the build.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;

-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'consumer_keys_app_environment_prefix_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX consumer_keys_app_environment_prefix_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'consumer_keys_app_environment_name_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX consumer_keys_app_environment_name_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'api_consumers_app_environment_external_ref_uniq' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX api_consumers_app_environment_external_ref_uniq';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'api_consumers_environment_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX api_consumers_environment_idx';
 END IF;
END $$;
-- +goose StatementEnd

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS consumer_keys_app_environment_prefix_idx
    ON consumer_keys (app_id, (COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'::uuid)), prefix);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS consumer_keys_app_environment_name_idx
    ON consumer_keys (account_id, app_id, (COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'::uuid)), name);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS api_consumers_app_environment_external_ref_uniq
    ON api_consumers (app_id, (COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'::uuid)), external_ref);

CREATE INDEX CONCURRENTLY IF NOT EXISTS api_consumers_environment_idx
    ON api_consumers (environment_id) WHERE environment_id IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS api_consumers_environment_idx;
DROP INDEX CONCURRENTLY IF EXISTS api_consumers_app_environment_external_ref_uniq;
DROP INDEX CONCURRENTLY IF EXISTS consumer_keys_app_environment_name_idx;
DROP INDEX CONCURRENTLY IF EXISTS consumer_keys_app_environment_prefix_idx;
