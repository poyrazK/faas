-- +goose Up
CREATE TABLE IF NOT EXISTS execution_outbound_integrations (
    execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    integration_id uuid NOT NULL,
    PRIMARY KEY (execution_id, integration_id)
);

-- +goose Down
DROP TABLE execution_outbound_integrations;
