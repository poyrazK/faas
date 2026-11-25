-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE FUNCTION reject_data_api_inspection_evidence_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Data API inspection evidence is immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER data_api_inspection_admission_immutable BEFORE UPDATE ON data_api_inspection_admissions FOR EACH ROW EXECUTE FUNCTION reject_data_api_inspection_evidence_update();
CREATE TRIGGER data_api_inspection_execution_immutable BEFORE UPDATE ON data_api_inspection_executions FOR EACH ROW EXECUTE FUNCTION reject_data_api_inspection_evidence_update();

-- +goose Down
DROP TRIGGER data_api_inspection_admission_immutable ON data_api_inspection_admissions;
DROP TRIGGER data_api_inspection_execution_immutable ON data_api_inspection_executions;
DROP FUNCTION reject_data_api_inspection_evidence_update();
