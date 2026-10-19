-- +goose Up
CREATE TABLE IF NOT EXISTS deployment_mcp_readiness_monitors (
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 role text NOT NULL,
 status jsonb NOT NULL DEFAULT '{}'::jsonb,
 next_probe_at timestamptz NOT NULL DEFAULT now(),
 lease_id uuid,
 lease_until timestamptz,
 PRIMARY KEY(deployment_id,role)
);
CREATE INDEX IF NOT EXISTS deployment_mcp_readiness_monitors_due ON deployment_mcp_readiness_monitors(next_probe_at);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM deployment_mcp_readiness_monitors) THEN RAISE EXCEPTION 'remove retained readiness monitoring history before rollback'; END IF;
END $$;
-- +goose StatementEnd
DROP TABLE deployment_mcp_readiness_monitors;
