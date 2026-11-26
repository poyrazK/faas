-- filename: 20261126210000000_api_sandbox_suite_release_gate.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE app_release_approval_policies
    ADD COLUMN require_passing_sandbox_suite boolean NOT NULL DEFAULT false;

CREATE TABLE api_sandbox_release_admissions (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    task_id uuid NOT NULL UNIQUE REFERENCES app_tasks(id) ON DELETE CASCADE,
    suite_run_id uuid NOT NULL
);

-- +goose Down
DROP TABLE api_sandbox_release_admissions;
ALTER TABLE app_release_approval_policies DROP COLUMN require_passing_sandbox_suite;
