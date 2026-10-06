-- adr: 623
-- +goose Up
-- +goose StatementBegin
ALTER TABLE project_environment_promotions
 ADD COLUMN IF NOT EXISTS bindings_required boolean NOT NULL DEFAULT false,
 ADD COLUMN IF NOT EXISTS bindings_check jsonb,
 ADD COLUMN IF NOT EXISTS binding_check_next_at timestamptz,
 ADD COLUMN IF NOT EXISTS binding_worker_token uuid,
 ADD COLUMN IF NOT EXISTS binding_worker_until timestamptz;
ALTER TABLE project_environment_promotions DROP CONSTRAINT IF EXISTS promotion_binding_worker_check;
ALTER TABLE project_environment_promotions ADD CONSTRAINT promotion_binding_worker_check CHECK (
 (binding_worker_token IS NULL) = (binding_worker_until IS NULL)
 AND (NOT bindings_required OR release_graph_mode)
 AND (bindings_check IS NULL OR jsonb_typeof(bindings_check)='object')
);
CREATE INDEX IF NOT EXISTS project_environment_binding_promotion_pending
 ON project_environment_promotions(binding_check_next_at,created_at,id)
 WHERE bindings_required AND status='running' AND target_release_set_id IS NULL;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS project_environment_binding_promotion_pending;
ALTER TABLE project_environment_promotions DROP CONSTRAINT IF EXISTS promotion_binding_worker_check;
ALTER TABLE project_environment_promotions
 DROP COLUMN IF EXISTS binding_worker_until, DROP COLUMN IF EXISTS binding_worker_token,
 DROP COLUMN IF EXISTS binding_check_next_at, DROP COLUMN IF EXISTS bindings_check, DROP COLUMN IF EXISTS bindings_required;
-- +goose StatementEnd
