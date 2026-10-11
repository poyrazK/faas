-- filename: 20261011230231722_workflow_playbook_progress.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS customer_workflow_playbook_progress (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
 case_key text NOT NULL,
 revision bigint NOT NULL CHECK (revision > 0),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object')
);
CREATE INDEX IF NOT EXISTS customer_workflow_playbook_progress_case ON customer_workflow_playbook_progress (account_id, app_id, tenant_id, case_key, id);

-- +goose Down
DROP TABLE IF EXISTS customer_workflow_playbook_progress;
