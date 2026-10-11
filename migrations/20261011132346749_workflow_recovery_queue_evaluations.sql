-- +goose Up
ALTER TABLE workflow_recovery_policy_executions ADD COLUMN last_evaluation jsonb
 CHECK(last_evaluation IS NULL OR (
  jsonb_typeof(last_evaluation)='object'
  AND (jsonb_typeof(last_evaluation->'evaluated_at')='string') IS TRUE
  AND (last_evaluation->>'reason' IN ('hourly_budget','cooldown','execution_quota','evidence_unknown','outstanding_retry','admission_wait','execution_wait','retry_running','inspection_error','child_created','result_recorded','suppressed')) IS TRUE
  AND (NOT last_evaluation ? 'waiting_since' OR jsonb_typeof(last_evaluation->'waiting_since')='string')
  AND (last_evaluation->>'reason' NOT IN ('child_created','result_recorded','suppressed') OR NOT last_evaluation ? 'waiting_since')
 ));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_executions WHERE last_evaluation IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot discard automatic recovery evaluation evidence';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE workflow_recovery_policy_executions DROP COLUMN last_evaluation;
