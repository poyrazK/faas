-- +goose Up
-- +goose StatementBegin
ALTER TABLE executions ADD COLUMN IF NOT EXISTS workflow_id text;
ALTER TABLE executions ADD COLUMN IF NOT EXISTS step_label text;

ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_workflow_id_check;
ALTER TABLE executions ADD CONSTRAINT executions_workflow_id_check CHECK (
    workflow_id IS NULL OR workflow_id ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,95}$'
);
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_step_label_check;
ALTER TABLE executions ADD CONSTRAINT executions_step_label_check CHECK (
    step_label IS NULL OR (
        workflow_id IS NOT NULL
        AND octet_length(step_label) BETWEEN 1 AND 128
        AND length(btrim(step_label)) = length(step_label)
        AND step_label !~ '[[:cntrl:]]'
    )
);

CREATE INDEX IF NOT EXISTS executions_account_workflow_principal_created_idx
    ON executions (account_id, workflow_id, runs_principal_id, created_at DESC, id DESC)
    WHERE workflow_id IS NOT NULL;

-- Agent workflow labels are deterministic step identities. Keep duplicate
-- admissions from parallel clients or retries from creating a second guest
-- run for the same workflow step.
CREATE UNIQUE INDEX IF NOT EXISTS executions_account_agent_workflow_step_uniq
    ON executions (account_id, runs_principal_id, workflow_id, step_label)
    WHERE runs_principal_id IS NOT NULL
      AND workflow_id IS NOT NULL
      AND step_label LIKE 'gwf:%';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS executions_account_workflow_principal_created_idx;
DROP INDEX IF EXISTS executions_account_agent_workflow_step_uniq;
ALTER TABLE executions
    DROP CONSTRAINT IF EXISTS executions_step_label_check,
    DROP CONSTRAINT IF EXISTS executions_workflow_id_check,
    DROP COLUMN IF EXISTS step_label,
    DROP COLUMN IF EXISTS workflow_id;
-- +goose StatementEnd
