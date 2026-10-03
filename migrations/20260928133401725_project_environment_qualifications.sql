-- filename: 20260928133401725_project_environment_qualifications.sql

-- +goose Up
-- Qualification receipts are immutable assertions about an exact active
-- release graph. Promotion checks freshness at read/execute time.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'project_release_sets_qualification_identity_uniq'
          AND conrelid = 'project_release_sets'::regclass
    ) THEN
        ALTER TABLE project_release_sets
            ADD CONSTRAINT project_release_sets_qualification_identity_uniq
            UNIQUE (id, project_id, environment_slug);
    END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS project_environment_qualifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_slug text NOT NULL,
    release_set_id uuid NOT NULL,
    status text NOT NULL,
    checks jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CONSTRAINT project_environment_qualifications_status_check
        CHECK (status IN ('passed', 'failed')),
    CONSTRAINT project_environment_qualifications_checks_shape
        CHECK (
            jsonb_typeof(checks) = 'array' AND jsonb_array_length(checks) = 2
            AND (checks @> '[{"name":"health","status":"passed"}]'::jsonb
                 OR checks @> '[{"name":"health","status":"failed"}]'::jsonb)
            AND (checks @> '[{"name":"smoke","status":"passed"}]'::jsonb
                 OR checks @> '[{"name":"smoke","status":"failed"}]'::jsonb)
            AND status = CASE
                WHEN checks @> '[{"name":"health","status":"failed"}]'::jsonb
                  OR checks @> '[{"name":"smoke","status":"failed"}]'::jsonb
                THEN 'failed' ELSE 'passed' END
        ),
    CONSTRAINT project_environment_qualifications_results_shape
        CHECK (
            checks @> '[{"name":"health","results":[{}]}]'::jsonb
            AND checks @> '[{"name":"smoke","results":[{}]}]'::jsonb
        ),
    CONSTRAINT project_environment_qualifications_expiry_check
        CHECK (expires_at > created_at AND expires_at <= created_at + interval '24 hours'),
    CONSTRAINT project_environment_qualifications_environment_fkey
        FOREIGN KEY (project_id, environment_slug)
        REFERENCES project_environments(project_id, slug) ON DELETE CASCADE,
    CONSTRAINT project_environment_qualifications_release_fkey
        FOREIGN KEY (release_set_id, project_id, environment_slug)
        REFERENCES project_release_sets(id, project_id, environment_slug) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS project_environment_qualifications_latest_idx
    ON project_environment_qualifications
       (account_id, project_id, environment_slug, release_set_id, created_at DESC, id DESC);

ALTER TABLE project_environment_promotions
    ADD COLUMN IF NOT EXISTS source_qualification_id uuid
    REFERENCES project_environment_qualifications(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE project_environment_promotions
    DROP COLUMN IF EXISTS source_qualification_id;
DROP TABLE IF EXISTS project_environment_qualifications;
ALTER TABLE project_release_sets
    DROP CONSTRAINT IF EXISTS project_release_sets_qualification_identity_uniq;
