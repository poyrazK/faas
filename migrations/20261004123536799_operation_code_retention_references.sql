-- filename: 20261004123536799_operation_code_retention_references.sql

-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS customer_operations_definition_retention_idx
    ON customer_operations(definition_id, expires_at);
CREATE INDEX IF NOT EXISTS customer_operations_release_retention_idx
    ON customer_operations((record->>'release_id'), expires_at);

-- These views derive private code retention from authoritative ownership and
-- operation lifetime. They do not extend the public revision-header deadline.
CREATE OR REPLACE VIEW customer_operation_retained_release_refs AS
SELECT DISTINCT rs.id AS release_id
FROM customer_operations o
JOIN customer_operation_definitions def ON def.id = o.definition_id
    AND def.account_id = o.account_id AND def.app_id = o.app_id
    AND def.deployment_id::text = o.record->>'deployment_id'
    AND def.scope = o.record->>'scope'
JOIN apps a ON a.id = def.app_id AND a.account_id = o.account_id AND a.status <> 'deleted'
JOIN deployments d ON d.id = def.deployment_id AND d.app_id = def.app_id AND d.scope = def.scope
JOIN project_release_sets rs ON rs.id::text = o.record->>'release_id'
    AND rs.account_id = o.account_id AND rs.project_id = a.project_id
    AND rs.environment_slug = def.scope
JOIN project_release_members source ON source.release_id = rs.id
    AND source.app_id = def.app_id AND source.deployment_id = def.deployment_id
WHERE o.state IN ('accepted', 'running') OR o.expires_at > now();

CREATE OR REPLACE VIEW customer_operation_retained_deployment_refs AS
SELECT DISTINCT def.deployment_id
FROM customer_operations o
JOIN customer_operation_definitions def ON def.id = o.definition_id
    AND def.account_id = o.account_id AND def.app_id = o.app_id
    AND def.deployment_id::text = o.record->>'deployment_id'
    AND def.scope = o.record->>'scope'
JOIN apps a ON a.id = def.app_id AND a.account_id = o.account_id AND a.status <> 'deleted'
JOIN deployments d ON d.id = def.deployment_id AND d.app_id = def.app_id AND d.scope = def.scope
WHERE o.state IN ('accepted', 'running') OR o.expires_at > now()
UNION
SELECT rm.deployment_id
FROM customer_operation_retained_release_refs retained
JOIN project_release_sets rs ON rs.id = retained.release_id
JOIN project_release_members rm ON rm.release_id = rs.id
JOIN apps a ON a.id = rm.app_id AND a.account_id = rs.account_id AND a.project_id = rs.project_id AND a.status <> 'deleted'
JOIN deployments d ON d.id = rm.deployment_id AND d.app_id = a.id AND d.scope = rs.environment_slug;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS customer_operation_retained_deployment_refs;
DROP VIEW IF EXISTS customer_operation_retained_release_refs;
DROP INDEX IF EXISTS customer_operations_definition_retention_idx;
DROP INDEX IF EXISTS customer_operations_release_retention_idx;
-- +goose StatementEnd
