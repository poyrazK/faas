-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Keep accepting v5 records during a rolling scheduler upgrade. Readers
-- must require v6 before using the trust fingerprint as evidence.
-- Upgrade the original v5 check only. On replay, preserve the existing
-- constraint (including any later strengthening) and existing v6 evidence.
-- +goose StatementBegin
DO $guard$
DECLARE existing_check text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO existing_check
 FROM pg_constraint
 WHERE conrelid = 'data_api_inspection_executions'::regclass
   AND conname = 'data_api_inspection_executions_protocol_version_check';
 IF existing_check = 'CHECK ((protocol_version = 5))' THEN
   ALTER TABLE data_api_inspection_executions
     DROP CONSTRAINT data_api_inspection_executions_protocol_version_check;
   existing_check := NULL;
 END IF;
 IF existing_check IS NULL THEN
   ALTER TABLE data_api_inspection_executions
     ADD CONSTRAINT data_api_inspection_executions_protocol_version_check
     CHECK (protocol_version IN (5, 6));
 END IF;
END
$guard$;
-- +goose StatementEnd

-- +goose Down
-- Refuse rollback while v6 evidence still exists rather than discard it.
ALTER TABLE data_api_inspection_executions
 DROP CONSTRAINT data_api_inspection_executions_protocol_version_check,
 ADD CONSTRAINT data_api_inspection_executions_protocol_version_check
 CHECK (protocol_version = 5);
