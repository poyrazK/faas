-- +goose Up
CREATE TABLE deployment_mcp_task_holds (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    detail text NOT NULL CHECK (octet_length(detail) <= 8192),
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE deployment_mcp_task_holds;
