-- filename: 20261114160000000_plan_fit_notices.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Plan-fit notices (billing review H17-4). schedd parks and refuses to wake
-- apps outside their billing subject's plan (ADR-20261103131742); meterd
-- emails the subject when that set of apps changes. One row per subject
-- (an account or a resource-owning organization) records the plan and the
-- digest of the over-plan app set last mailed; the row is removed when the
-- subject fits its plan again.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS plan_fit_notices (
    subject_id  uuid PRIMARY KEY,
    plan        text NOT NULL CHECK (plan IN ('free', 'hobby', 'pro', 'scale')),
    apps_digest text NOT NULL CHECK (apps_digest ~ '^[0-9a-f]{64}$'),
    noticed_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

COMMENT ON TABLE plan_fit_notices IS
    'Last plan-fit email per billing subject: the plan and the sha256 of its over-plan app set. Written by meterd (billing review H17-4).';

-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON plan_fit_notices TO faas_runtime;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS plan_fit_notices;
