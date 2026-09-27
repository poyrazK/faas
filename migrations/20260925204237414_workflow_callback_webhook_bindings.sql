-- filename: 20260925204237414_workflow_callback_webhook_bindings.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-264. Reuse ADR-212's provider-verified ingress and its sealed secret.
-- A single Stripe object event can resolve only one workflow callback.
CREATE TABLE IF NOT EXISTS workflow_callback_webhook_bindings (
    id uuid PRIMARY KEY,
    endpoint_id uuid NOT NULL REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    run_id uuid NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    step_name text NOT NULL CHECK (char_length(step_name) >= 1),
    event_type text NOT NULL CHECK (char_length(event_type) <= 256 AND event_type ~ '^[a-z][a-z0-9_.]*$'),
    object_id text NOT NULL CHECK (char_length(object_id) BETWEEN 1 AND 256 AND object_id ~ '^[A-Za-z0-9_-]+$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT workflow_callback_webhook_bindings_step_uniq UNIQUE (run_id, step_name),
    CONSTRAINT workflow_callback_webhook_bindings_match_uniq UNIQUE (endpoint_id, event_type, object_id)
);
CREATE INDEX IF NOT EXISTS workflow_callback_webhook_bindings_endpoint_idx
    ON workflow_callback_webhook_bindings (endpoint_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS workflow_callback_webhook_bindings;
-- +goose StatementEnd
