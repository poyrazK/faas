-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS workflow_initial_recovery_reviews (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 failure_id uuid NOT NULL,
 policy_version bigint NOT NULL CHECK(policy_version>0),
 prepared_at timestamptz NOT NULL CHECK(isfinite(prepared_at)),
 expires_at timestamptz NOT NULL CHECK(isfinite(expires_at) AND expires_at>prepared_at),
 submitted_at timestamptz CHECK(submitted_at IS NULL OR isfinite(submitted_at)),
 job_id uuid,
 review jsonb NOT NULL CHECK((jsonb_typeof(review)='object'
  AND review->>'status' IN ('prepared','submitted','cancelled','expired','stale')
  AND review->>'id'=id::text AND review->>'app_id'=app_id::text
  AND review->'failure'->>'id'=failure_id::text
  AND (review->>'policy_version')::bigint=policy_version
  AND (review->>'prepared_at')::timestamptz=prepared_at
  AND (review->>'expires_at')::timestamptz=expires_at
  AND jsonb_typeof(review->'events')='array' AND jsonb_array_length(review->'events')>0
  AND ((review->>'status'='submitted')=(job_id IS NOT NULL))
  AND (job_id IS NULL OR review->>'job_id'=job_id::text AND (review->>'submitted_at')::timestamptz=submitted_at)
 ) IS TRUE),
 UNIQUE(app_id,failure_id,policy_version),
 CHECK((job_id IS NULL)=(submitted_at IS NULL))
);
CREATE INDEX IF NOT EXISTS workflow_initial_recovery_reviews_app_time ON workflow_initial_recovery_reviews(app_id,prepared_at);
CREATE UNIQUE INDEX IF NOT EXISTS workflow_initial_recovery_reviews_job ON workflow_initial_recovery_reviews(job_id) WHERE job_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_initial_recovery_reviews) THEN RAISE EXCEPTION 'cannot downgrade while initial recovery reviews are retained'; END IF;
END $$;
DROP TABLE workflow_initial_recovery_reviews;
-- +goose StatementEnd
