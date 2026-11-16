-- filename: 20261116094500000_env_keys_mixed_case.sql
-- migration-compat: expand
-- Env var names accept mixed case (api.EnvKeyPattern ^[A-Za-z][A-Za-z0-9_]*$):
-- runtimes read names like DOTNET_EnableDiagnostics (microservices-demo,
-- 2026-11-16). Widened: app_envs keys, config-revision keys, and the env key
-- a secret reference is delivered under. Secret NAMES stay uppercase, as do
-- scoped environment workload variables. Each wider CHECK is added NOT VALID
-- under a new name and validated, then the old one dropped; re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $widen1$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'app_envs_key_shape_v2') THEN
    ALTER TABLE app_envs ADD CONSTRAINT app_envs_key_shape_v2
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$widen1$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $widen2$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_config_revisions'::regclass AND conname = 'app_config_revisions_config_key_v2_check') THEN
    ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_config_key_v2_check
      CHECK (config_key = '' OR (config_key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND length(config_key) <= 128)) NOT VALID;
  END IF;
END
$widen2$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $widen3$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'app_environment_secret_refs_key_v2_check') THEN
    ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_key_v2_check
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND octet_length(key) <= 128) NOT VALID;
  END IF;
END
$widen3$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $widen4$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'app_environment_secret_ref_suppressions_key_v2_check') THEN
    ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_key_v2_check
      CHECK (key ~ '^[A-Za-z][A-Za-z0-9_]*$' AND octet_length(key) <= 128) NOT VALID;
  END IF;
END
$widen4$;
-- +goose StatementEnd

ALTER TABLE app_envs VALIDATE CONSTRAINT app_envs_key_shape_v2;
ALTER TABLE app_config_revisions VALIDATE CONSTRAINT app_config_revisions_config_key_v2_check;
ALTER TABLE app_environment_secret_refs VALIDATE CONSTRAINT app_environment_secret_refs_key_v2_check;
ALTER TABLE app_environment_secret_ref_suppressions VALIDATE CONSTRAINT app_environment_secret_ref_suppressions_key_v2_check;

ALTER TABLE app_envs DROP CONSTRAINT IF EXISTS app_envs_key_shape;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_config_key_check;
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_key_check;
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT IF EXISTS app_environment_secret_ref_suppressions_key_check;

-- +goose Down
SET lock_timeout = '5s';

-- Mixed-case rows written since stay; the restored uppercase CHECKs are
-- NOT VALID so the rollback does not delete customer configuration, and
-- they refuse new mixed-case writes again.
-- +goose StatementBegin
DO $restore1$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_envs'::regclass AND conname = 'app_envs_key_shape') THEN
    ALTER TABLE app_envs ADD CONSTRAINT app_envs_key_shape
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128) NOT VALID;
  END IF;
END
$restore1$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $restore2$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_config_revisions'::regclass AND conname = 'app_config_revisions_config_key_check') THEN
    ALTER TABLE app_config_revisions ADD CONSTRAINT app_config_revisions_config_key_check
      CHECK (config_key = '' OR (config_key ~ '^[A-Z][A-Z0-9_]*$' AND length(config_key) <= 128)) NOT VALID;
  END IF;
END
$restore2$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $restore3$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_refs'::regclass AND conname = 'app_environment_secret_refs_key_check') THEN
    ALTER TABLE app_environment_secret_refs ADD CONSTRAINT app_environment_secret_refs_key_check
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND octet_length(key) <= 128) NOT VALID;
  END IF;
END
$restore3$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $restore4$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_environment_secret_ref_suppressions'::regclass AND conname = 'app_environment_secret_ref_suppressions_key_check') THEN
    ALTER TABLE app_environment_secret_ref_suppressions ADD CONSTRAINT app_environment_secret_ref_suppressions_key_check
      CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND octet_length(key) <= 128) NOT VALID;
  END IF;
END
$restore4$;
-- +goose StatementEnd

ALTER TABLE app_envs DROP CONSTRAINT IF EXISTS app_envs_key_shape_v2;
ALTER TABLE app_config_revisions DROP CONSTRAINT IF EXISTS app_config_revisions_config_key_v2_check;
ALTER TABLE app_environment_secret_refs DROP CONSTRAINT IF EXISTS app_environment_secret_refs_key_v2_check;
ALTER TABLE app_environment_secret_ref_suppressions DROP CONSTRAINT IF EXISTS app_environment_secret_ref_suppressions_key_v2_check;
