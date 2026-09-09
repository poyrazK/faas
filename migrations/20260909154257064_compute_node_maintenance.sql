-- +goose Up
-- A completed operator drain must remain non-admitting until the operator
-- explicitly activates the node after maintenance. PostgreSQL enum values
-- cannot be removed safely while rows may reference them, so the down
-- migration is intentionally a no-op.
ALTER TYPE compute_node_lifecycle ADD VALUE IF NOT EXISTS 'force_draining';
ALTER TYPE compute_node_lifecycle ADD VALUE IF NOT EXISTS 'maintenance';

-- +goose Down
SELECT 1;
