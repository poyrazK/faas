-- filename: 20261018003150916_function_migration_admission.sql
-- adr: 1663. Fresh permissions remain attached to the original durable source.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE function_migration_sources
 ADD COLUMN IF NOT EXISTS capture_token uuid UNIQUE,
 ADD COLUMN IF NOT EXISTS capture_grant jsonb,
 ADD COLUMN IF NOT EXISTS recovery_grant jsonb;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='function_migration_sources'::regclass AND conname='function_migration_permission_pair') THEN
  ALTER TABLE function_migration_sources ADD CONSTRAINT function_migration_permission_pair CHECK ((
   (capture_token IS NULL AND capture_grant IS NULL AND recovery_grant IS NULL)
   OR (capture_token IS NOT NULL AND capture_grant IS NOT NULL
    AND jsonb_typeof(capture_grant)='object' AND capture_grant->>'version'='1'
    AND capture_grant->>'purpose'='capture' AND capture_grant->>'token'=capture_token::text
    AND capture_grant->>'capture_token'=capture_token::text
    AND capture_grant->>'lease_token'=lease_token AND capture_grant->>'source_wake_id'=source_wake_id::text
    AND capture_grant->'parent'->'binding'->>'instance_id'=instance_id::text
    AND capture_grant->'parent'->'binding'->>'node_id'=source_node_id
    AND (recovery_grant IS NULL OR (jsonb_typeof(recovery_grant)='object'
     AND recovery_grant->>'version'='1' AND recovery_grant->>'purpose'='recover'
     AND recovery_grant->>'capture_token'=capture_token::text
     AND recovery_grant->>'lease_token'=lease_token AND recovery_grant->>'source_wake_id'=source_wake_id::text
     AND recovery_grant->'parent'=capture_grant->'parent')))) IS TRUE);
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM function_migration_sources WHERE capture_token IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove function migration permissions while admitted sources remain';
 END IF;
END $$;
ALTER TABLE function_migration_sources DROP CONSTRAINT function_migration_permission_pair,
 DROP COLUMN recovery_grant, DROP COLUMN capture_grant, DROP COLUMN capture_token;
-- +goose StatementEnd
