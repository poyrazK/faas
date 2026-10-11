-- filename: 20261003150000001_workflow_operation_effects.sql
-- +goose Up
-- +goose StatementBegin
-- Effect identity and receiver metadata survive workflow-run retention and
-- webhook history pruning. The row is inserted in the same transaction that
-- marks its step successful; id is also the app_webhook_deliveries ID.
CREATE TABLE IF NOT EXISTS workflow_operation_effects (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    run_id uuid NOT NULL,
    step_name text NOT NULL,
    operation_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    payload jsonb NOT NULL,
    webhook_id uuid NOT NULL,
    event_type text NOT NULL CHECK (
        octet_length(event_type) BETWEEN 1 AND 256
        AND event_type ~ '^[a-z][a-z0-9_.-]*$'
    ),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (operation_id, name)
);

CREATE INDEX IF NOT EXISTS workflow_operation_effects_attempt_idx
    ON workflow_operation_effects (run_id, step_name, generation, name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Retain accepted effect/delivery identities; this is a forward-only ledger.
SELECT 1;
-- +goose StatementEnd
