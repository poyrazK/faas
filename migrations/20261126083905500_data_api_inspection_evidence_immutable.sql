-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $guard$
BEGIN
 IF to_regprocedure(format('%I.reject_data_api_inspection_evidence_update()', current_schema())) IS NULL THEN
  EXECUTE $create$CREATE FUNCTION reject_data_api_inspection_evidence_update() RETURNS trigger LANGUAGE plpgsql AS $body$
BEGIN
  RAISE EXCEPTION 'Data API inspection evidence is immutable' USING ERRCODE = '23514';
END;
$body$$create$;
 END IF;
END
$guard$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $guard$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'data_api_inspection_admissions'::regclass AND tgname = 'data_api_inspection_admission_immutable' AND NOT tgisinternal) THEN
  CREATE TRIGGER data_api_inspection_admission_immutable BEFORE UPDATE ON data_api_inspection_admissions FOR EACH ROW EXECUTE FUNCTION reject_data_api_inspection_evidence_update();
 END IF;
END
$guard$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $guard$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid = 'data_api_inspection_executions'::regclass AND tgname = 'data_api_inspection_execution_immutable' AND NOT tgisinternal) THEN
  CREATE TRIGGER data_api_inspection_execution_immutable BEFORE UPDATE ON data_api_inspection_executions FOR EACH ROW EXECUTE FUNCTION reject_data_api_inspection_evidence_update();
 END IF;
END
$guard$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER data_api_inspection_admission_immutable ON data_api_inspection_admissions;
DROP TRIGGER data_api_inspection_execution_immutable ON data_api_inspection_executions;
DROP FUNCTION reject_data_api_inspection_evidence_update();
