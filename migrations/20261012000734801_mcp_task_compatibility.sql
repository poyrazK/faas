-- Store one bounded, payload-free MCP handler-compatibility snapshot per app.
-- The observer replaces this row every 15 seconds; the API computes freshness
-- from observed_at and keeps only the latest snapshot.
-- +goose Up
CREATE TABLE IF NOT EXISTS app_mcp_task_compatibility (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    handlers jsonb NOT NULL,
    truncated boolean NOT NULL DEFAULT false,
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (app_id),
    CONSTRAINT app_mcp_task_compatibility_handlers_shape
        CHECK (jsonb_typeof(handlers) = 'array' AND jsonb_array_length(handlers) <= 256),
    CONSTRAINT app_mcp_task_compatibility_handlers_size
        CHECK (octet_length(handlers::text) <= 131072)
);

-- +goose Down
DROP TABLE IF EXISTS app_mcp_task_compatibility;
