-- +goose Up
ALTER TABLE workflow_initial_recovery_reviews ADD COLUMN IF NOT EXISTS completion_next_observe_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(completion_next_observe_at));
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='workflow_initial_recovery_reviews'::regclass AND conname='workflow_initial_manual_result_check') THEN
 ALTER TABLE workflow_initial_recovery_reviews ADD CONSTRAINT workflow_initial_manual_result_check CHECK((
 NOT review ? 'result' OR (review->'policy'->>'mode'='preview' AND job_id IS NOT NULL
 AND NOT review ? 'automatic_result' AND jsonb_typeof(review->'result')='object'
 AND (review->'result'->>'complete')::boolean
 AND review->'result'->>'outcome' IN ('succeeded','failed','cancelled')
 AND review->'result'->>'run_id'=review->'target'->>'run_id'
 AND (review->'result'->>'resume_count')::integer=1
 AND review->'result'->>'child_job_id'=job_id::text)) IS TRUE);
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS workflow_initial_manual_completion_due ON workflow_initial_recovery_reviews(completion_next_observe_at,id) WHERE submitted_at IS NOT NULL AND review->'policy'->>'mode'='preview' AND NOT review ? 'result';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_initial_recovery_reviews WHERE review ? 'result') THEN RAISE EXCEPTION 'manual initial completion results are retained'; END IF;
END $$;
-- +goose StatementEnd
DROP INDEX IF EXISTS workflow_initial_manual_completion_due;
ALTER TABLE workflow_initial_recovery_reviews DROP CONSTRAINT IF EXISTS workflow_initial_manual_result_check;
ALTER TABLE workflow_initial_recovery_reviews DROP COLUMN IF EXISTS completion_next_observe_at;
