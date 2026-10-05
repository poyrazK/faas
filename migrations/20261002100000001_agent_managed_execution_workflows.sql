-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS agent_execution_workflows (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    runs_principal_id uuid,
    workflow_id text NOT NULL,
    plan_id text NOT NULL,
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    step_count smallint NOT NULL CHECK (step_count BETWEEN 1 AND 16),
    next_step smallint NOT NULL DEFAULT 0,
    sealed_plan bytea,
    payload_kid text NOT NULL DEFAULT '',
    lease_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    scheduled_for timestamptz NOT NULL DEFAULT now(),
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CONSTRAINT agent_execution_workflows_plan_id_check
        CHECK (plan_id ~ '^[0-9a-f]{24}$'),
    CONSTRAINT agent_execution_workflows_id_check
        CHECK (length(workflow_id) BETWEEN 1 AND 96),
    CONSTRAINT agent_execution_workflows_next_step_check
        CHECK (next_step BETWEEN 0 AND step_count),
    CONSTRAINT agent_execution_workflows_sealed_plan_check
        CHECK (sealed_plan IS NULL OR octet_length(sealed_plan) BETWEEN 1 AND 4259840),
    CONSTRAINT agent_execution_workflows_payload_kid_check
        CHECK ((payload_kid = '' AND status IN ('succeeded', 'failed'))
            OR (length(payload_kid) BETWEEN 1 AND 255 AND status IN ('queued', 'running'))),
    CONSTRAINT agent_execution_workflows_plan_retention_check
        CHECK ((status IN ('succeeded', 'failed')) = (sealed_plan IS NULL)),
    CONSTRAINT agent_execution_workflows_error_check
        CHECK (length(last_error) <= 2048),
    CONSTRAINT agent_execution_workflows_lease_pair_check
        CHECK ((lease_token IS NULL) = (lease_owner IS NULL)
            AND (lease_owner IS NULL) = (lease_expires_at IS NULL)),
    CONSTRAINT agent_execution_workflows_finish_check
        CHECK ((status IN ('succeeded', 'failed')) = (finished_at IS NOT NULL)),
    CONSTRAINT agent_execution_workflows_updated_check
        CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS agent_execution_workflows_principal_key
    ON agent_execution_workflows (account_id, runs_principal_id, workflow_id)
    WHERE runs_principal_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS agent_execution_workflows_account_key
    ON agent_execution_workflows (account_id, workflow_id)
    WHERE runs_principal_id IS NULL;
CREATE INDEX IF NOT EXISTS agent_execution_workflows_account_active_idx
    ON agent_execution_workflows (account_id)
    WHERE status IN ('queued', 'running');
CREATE INDEX IF NOT EXISTS agent_execution_workflows_account_workflow_idx
    ON agent_execution_workflows (account_id, workflow_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_execution_workflows_claim_idx
    ON agent_execution_workflows (scheduled_for, created_at)
    WHERE status IN ('queued', 'running');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_execution_workflows;
-- +goose StatementEnd
