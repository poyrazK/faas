-- adr: 693
-- +goose Up
CREATE TABLE runtime_upgrade_gateway_receipts (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 gateway_session_id uuid NOT NULL,
 deployment_id uuid NOT NULL REFERENCES deployment_runtime_upgrade_cutovers(deployment_id) ON DELETE CASCADE,
 cutover_at timestamptz NOT NULL,
 installed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(app_id,gateway_session_id),
 CHECK (installed_at >= cutover_at)
);
CREATE INDEX runtime_upgrade_gateway_receipts_expiry ON runtime_upgrade_gateway_receipts(installed_at);

-- +goose Down
DROP TABLE runtime_upgrade_gateway_receipts;
