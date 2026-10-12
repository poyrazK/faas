-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF to_regclass('workflow_recovery_policy_targets_budget') IS NOT NULL AND to_regclass('workflow_recovery_policy_reviews_app') IS NOT NULL AND to_regclass('workflow_recovery_policies_due') IS NOT NULL THEN
  RETURN;
 END IF;
CREATE TABLE workflow_recovery_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 policy jsonb NOT NULL CHECK(jsonb_typeof(policy)='object' AND policy->>'mode'='preview'),
 updated_at timestamptz NOT NULL,
 actor_kind text NOT NULL,
 actor_id text NOT NULL,
 next_evaluate_at timestamptz NOT NULL,
 last_evaluated_at timestamptz,
 job_cursor uuid,
 status text NOT NULL CHECK(status IN ('disabled','monitoring','review_limit'))
);
CREATE INDEX workflow_recovery_policies_due ON workflow_recovery_policies(next_evaluate_at,app_id) WHERE enabled;
CREATE TABLE workflow_recovery_policy_reviews (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 job_id uuid NOT NULL REFERENCES event_recovery_jobs(id) ON DELETE CASCADE,
 fingerprint text NOT NULL,
 review jsonb NOT NULL CHECK(jsonb_typeof(review)='object'),
 observed_at timestamptz NOT NULL,
 UNIQUE(app_id,job_id,fingerprint)
);
CREATE INDEX workflow_recovery_policy_reviews_app ON workflow_recovery_policy_reviews(app_id,id);
CREATE TABLE workflow_recovery_policy_targets (
 app_id uuid NOT NULL REFERENCES workflow_recovery_policies(app_id) ON DELETE CASCADE,
 review_id uuid NOT NULL REFERENCES workflow_recovery_policy_reviews(id) ON DELETE CASCADE,
 run_id uuid NOT NULL,
 resume_count integer NOT NULL CHECK(resume_count BETWEEN 1 AND 16),
 action text NOT NULL CHECK(action IN ('cancel','retry')),
 prepared_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,run_id,resume_count,action)
);
CREATE INDEX workflow_recovery_policy_targets_budget ON workflow_recovery_policy_targets(app_id,prepared_at);
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_policy_reviews) THEN RAISE EXCEPTION 'cannot downgrade while policy reviews are retained'; END IF;
 IF EXISTS(SELECT 1 FROM workflow_recovery_policies) THEN RAISE EXCEPTION 'cannot downgrade while recovery policies are retained'; END IF;
END $$;
DROP TABLE workflow_recovery_policy_targets;
DROP TABLE workflow_recovery_policy_reviews;
DROP TABLE workflow_recovery_policies;
-- +goose StatementEnd
