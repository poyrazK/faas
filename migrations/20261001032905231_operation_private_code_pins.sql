-- filename: 20261001032905231_operation_private_code_pins.sql

-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS deployments_operation_code_pin_owner_idx ON deployments(id,app_id);
CREATE TABLE IF NOT EXISTS customer_operation_code_pins (
    deployment_id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
    CONSTRAINT customer_operation_code_pins_owner_fk FOREIGN KEY(deployment_id,app_id)
        REFERENCES deployments(id,app_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS customer_operation_code_pins_app_expiry_idx ON customer_operation_code_pins(app_id,expires_at);
CREATE INDEX IF NOT EXISTS customer_operation_code_pins_expiry_idx ON customer_operation_code_pins(expires_at);

-- Preserve existing public deadlines. Only owned operation references seed
-- private cleanup receipts; these survive projection GC until ordinary expiry.
WITH owned_operations AS (
    SELECT o.expires_at,o.account_id,def.app_id,def.deployment_id,def.scope,a.project_id,o.record->>'release_id' AS release_id
    FROM customer_operations o JOIN customer_operation_definitions def ON def.id=o.definition_id
        AND def.account_id=o.account_id AND def.app_id=o.app_id
        AND def.deployment_id::text=o.record->>'deployment_id' AND def.scope=o.record->>'scope'
    JOIN apps a ON a.id=def.app_id AND a.account_id=o.account_id AND a.status<>'deleted'
    JOIN deployments d ON d.id=def.deployment_id AND d.app_id=def.app_id AND d.scope=def.scope
    WHERE o.state IN ('accepted','running') OR o.expires_at>now()
), code_refs AS (
    SELECT deployment_id,app_id,expires_at FROM owned_operations
    UNION ALL
    SELECT rm.deployment_id,rm.app_id,o.expires_at FROM owned_operations o
    JOIN project_release_sets rs ON rs.id::text=o.release_id AND rs.account_id=o.account_id
        AND rs.project_id=o.project_id AND rs.environment_slug=o.scope
    JOIN project_release_members source ON source.release_id=rs.id AND source.app_id=o.app_id AND source.deployment_id=o.deployment_id
    JOIN project_release_members rm ON rm.release_id=rs.id
    JOIN apps a ON a.id=rm.app_id AND a.account_id=o.account_id AND a.project_id=rs.project_id AND a.status<>'deleted'
    JOIN deployments d ON d.id=rm.deployment_id AND d.app_id=rm.app_id AND d.scope=rs.environment_slug
)
INSERT INTO customer_operation_code_pins(deployment_id,app_id,expires_at)
SELECT deployment_id,app_id,max(expires_at) FROM code_refs GROUP BY deployment_id,app_id
ON CONFLICT(deployment_id) DO UPDATE SET expires_at=greatest(customer_operation_code_pins.expires_at,excluded.expires_at);

CREATE OR REPLACE VIEW deployment_code_pin_deadlines AS
SELECT deployment_id,app_id,max(expires_at) AS expires_at FROM (
    SELECT deployment_id,app_id,expires_at FROM deployment_revision_pins
    UNION ALL
    SELECT deployment_id,app_id,expires_at FROM customer_operation_code_pins
) receipts GROUP BY deployment_id,app_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS deployment_code_pin_deadlines;
DROP TABLE IF EXISTS customer_operation_code_pins;
DROP INDEX IF EXISTS deployments_operation_code_pin_owner_idx;
-- +goose StatementEnd
