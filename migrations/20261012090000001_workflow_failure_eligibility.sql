-- +goose Up
-- +goose StatementBegin
ALTER TABLE workflow_recovery_failure_evidence
 ADD COLUMN eligibility_observed_at timestamptz CHECK (isfinite(eligibility_observed_at) AND eligibility_observed_at>=observed_at),
 ADD COLUMN eligibility jsonb,
 ADD CONSTRAINT workflow_failure_eligibility_shape CHECK (
 (eligibility IS NULL AND eligibility_observed_at IS NULL) OR
 (eligibility IS NOT NULL AND eligibility_observed_at IS NOT NULL AND
 jsonb_typeof(eligibility)='object' AND jsonb_typeof(eligibility->'blockers')='array' AND
 eligibility ? 'reason' AND eligibility ? 'observed_at' AND
 eligibility->>'reason' IN ('eligible','capacity','resume_ineligible','resume_limit_reached','run_not_retained','changed','parent_not_retained','inspection_error') AND
 (eligibility->>'observed_at')::timestamptz=eligibility_observed_at));
CREATE INDEX workflow_failure_eligibility_queue_idx
 ON workflow_recovery_failure_evidence (eligibility_observed_at NULLS FIRST,observed_at,id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_failure_evidence WHERE eligibility IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove retained workflow failure eligibility observations';
 END IF;
END $$;
DROP INDEX workflow_failure_eligibility_queue_idx;
ALTER TABLE workflow_recovery_failure_evidence DROP CONSTRAINT workflow_failure_eligibility_shape,
 DROP COLUMN eligibility, DROP COLUMN eligibility_observed_at;
-- +goose StatementEnd
