-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF to_regclass('deployment_audit_mcp_admission_id_idx') IS NOT NULL THEN RETURN; END IF;
DO $$ DECLARE previous_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO previous_check FROM pg_constraint WHERE conrelid='deployment_audit'::regclass AND conname='deployment_audit_kind_chk';
 EXECUTE format('CREATE FUNCTION mcp_admission_audit_kind_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT %L',previous_check));
 ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
 EXECUTE format('ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk CHECK ((%s) OR kind = ''deploy.mcp_admission'')',regexp_replace(previous_check,'^CHECK \((.*)\)$','\1'));
END $$;
CREATE UNIQUE INDEX deployment_audit_mcp_admission_id_idx ON deployment_audit ((data->'mcp_admission'->>'id')) WHERE kind='deploy.mcp_admission';
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
DROP INDEX deployment_audit_mcp_admission_id_idx;
DELETE FROM deployment_audit WHERE kind='deploy.mcp_admission';
-- +goose StatementBegin
DO $$ BEGIN
 ALTER TABLE deployment_audit DROP CONSTRAINT deployment_audit_kind_chk;
 EXECUTE 'ALTER TABLE deployment_audit ADD CONSTRAINT deployment_audit_kind_chk '||mcp_admission_audit_kind_base();
END $$;
-- +goose StatementEnd
DROP FUNCTION mcp_admission_audit_kind_base();
