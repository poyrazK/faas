-- +goose Up
-- +goose StatementBegin
DO $replay_guard$
BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='workflow_initial_recovery_reviews'::regclass AND conname='workflow_initial_automatic_result_check') THEN RETURN; END IF;
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_policy_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_policy_check CHECK(jsonb_typeof(policy)='object' AND policy->>'mode' IN ('preview','automatic_retry','automatic_initial'));
ALTER TABLE workflow_initial_recovery_reviews ADD CONSTRAINT workflow_initial_automatic_result_check CHECK((NOT review ? 'automatic_result' OR (
 review->'policy'->>'mode'='automatic_initial' AND job_id IS NOT NULL
 AND jsonb_typeof(review->'automatic_result')='object'
 AND (review->'automatic_result'->>'complete')::boolean
 AND review->'automatic_result'->>'outcome' IN ('succeeded','failed','cancelled')
 AND review->'automatic_result'->>'run_id'=review->'target'->>'run_id'
 AND (review->'automatic_result'->>'resume_count')::integer=1
 AND review->'automatic_result'->>'child_job_id'=job_id::text
)) IS TRUE);
END $replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policies WHERE policy->>'mode'='automatic_initial')
 OR EXISTS(SELECT 1 FROM workflow_initial_recovery_reviews WHERE review->'policy'->>'mode'='automatic_initial') THEN
  RAISE EXCEPTION 'cannot downgrade while automatic initial recovery authority or history is retained';
 END IF;
END $$;
ALTER TABLE workflow_initial_recovery_reviews DROP CONSTRAINT workflow_initial_automatic_result_check;
ALTER TABLE workflow_recovery_policies DROP CONSTRAINT workflow_recovery_policies_policy_check;
ALTER TABLE workflow_recovery_policies ADD CONSTRAINT workflow_recovery_policies_policy_check CHECK(jsonb_typeof(policy)='object' AND policy->>'mode' IN ('preview','automatic_retry'));
-- +goose StatementEnd
