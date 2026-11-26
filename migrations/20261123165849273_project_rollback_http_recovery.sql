-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS project_rollback_http_recovery (
    rollback_id uuid PRIMARY KEY REFERENCES deployment_rollback_operations(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL REFERENCES project_rollback_operations(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    target_id uuid NOT NULL,
    service boolean NOT NULL,
    generation bigint UNIQUE,
    routing_committed_at timestamptz,
    expected_gateways text[] NOT NULL DEFAULT '{}',
    acknowledged_gateways text[] NOT NULL DEFAULT '{}',
    gateways_applied_at timestamptz,
    first_success_at timestamptz,
    expires_at timestamptz,
    next_notify_at timestamptz
);

CREATE INDEX IF NOT EXISTS project_rollback_http_recovery_operation
    ON project_rollback_http_recovery (operation_id);
CREATE INDEX IF NOT EXISTS project_rollback_http_recovery_pending
    ON project_rollback_http_recovery (next_notify_at, generation)
    WHERE next_notify_at IS NOT NULL;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE project_rollback_http_recovery;
