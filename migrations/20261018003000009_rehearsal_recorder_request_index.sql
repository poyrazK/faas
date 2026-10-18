-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_requests_session_status') AND relkind IN ('i','I')) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_requests_session_status') AND relkind IN ('i','I')) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 9; inspect recovery before continuing';
  END IF;

CREATE INDEX scenario_recorder_requests_session_status ON scenario_recorder_requests(session_id,status);
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP INDEX scenario_recorder_requests_session_status;
