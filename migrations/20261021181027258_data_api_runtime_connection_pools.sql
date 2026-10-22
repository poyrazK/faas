-- filename: 20261021181027258_data_api_runtime_connection_pools.sql
-- migration-compat: expand
--
-- ADR-20261021180534: a Data API runtime's engine pool is configurable from 1
-- to 8 connections, so its connection reports total 1 to 8 instead of exactly
-- 2. Runtimes that keep the default still write rows both CHECKs accept.
--
-- The CHECKs are widened online under new names: each is added NOT VALID,
-- validated, and only then is the old one dropped. Each step is its own
-- transaction; a VALIDATE takes SHARE UPDATE EXCLUSIVE, so reports keep
-- arriving during the scans, and a CHECK always holds. Every step is
-- re-runnable (TestNewMigrationsAreReplaySafe).
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $runtime_pools$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'data_api_runtime_connection_observations'::regclass AND conname = 'data_api_runtime_connection_observations_engine_pool_check') THEN
    ALTER TABLE data_api_runtime_connection_observations ADD CONSTRAINT data_api_runtime_connection_observations_engine_pool_check
      CHECK (engine_connections BETWEEN 1 AND 8) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'data_api_runtime_connection_observations'::regclass AND conname = 'data_api_runtime_connection_observations_pool_check') THEN
    ALTER TABLE data_api_runtime_connection_observations ADD CONSTRAINT data_api_runtime_connection_observations_pool_check
      CHECK (engine_connections + refresh_connections BETWEEN 1 AND 8) NOT VALID;
  END IF;
END
$runtime_pools$;
-- +goose StatementEnd

ALTER TABLE data_api_runtime_connection_observations VALIDATE CONSTRAINT data_api_runtime_connection_observations_engine_pool_check;

ALTER TABLE data_api_runtime_connection_observations VALIDATE CONSTRAINT data_api_runtime_connection_observations_pool_check;

ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observatio_engine_connections_check;

ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observations_check;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- Reports expire within 30 seconds; rows outside the old shape are dropped.
DELETE FROM data_api_runtime_connection_observations WHERE engine_connections > 2 OR engine_connections + refresh_connections <> 2;

-- +goose StatementBegin
DO $runtime_pools$
BEGIN
  ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observations_engine_pool_check;
  ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observations_pool_check;
  ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observatio_engine_connections_check;
  ALTER TABLE data_api_runtime_connection_observations DROP CONSTRAINT IF EXISTS data_api_runtime_connection_observations_check;
  ALTER TABLE data_api_runtime_connection_observations ADD CONSTRAINT data_api_runtime_connection_observatio_engine_connections_check
    CHECK (engine_connections BETWEEN 1 AND 2);
  ALTER TABLE data_api_runtime_connection_observations ADD CONSTRAINT data_api_runtime_connection_observations_check
    CHECK (engine_connections + refresh_connections = 2);
END
$runtime_pools$;
-- +goose StatementEnd

RESET lock_timeout;
