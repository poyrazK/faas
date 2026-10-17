-- filename: 20261017080018644_project_migration_runtime_lock_order.sql

-- +goose Up
-- Retirement must serialize with contract admission before the native runtime
-- guard takes its NOWAIT app fence. Otherwise a wake can be refused before it
-- reaches the project lock and re-reads the committed retirement decision.
DROP TRIGGER IF EXISTS project_migration_instance_fence ON instances;
CREATE OR REPLACE TRIGGER aa_project_migration_instance_fence
BEFORE INSERT OR UPDATE OF state,deployment_id ON instances
FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();

-- +goose Down
DROP TRIGGER IF EXISTS aa_project_migration_instance_fence ON instances;
CREATE OR REPLACE TRIGGER project_migration_instance_fence
BEFORE INSERT OR UPDATE OF state,deployment_id ON instances
FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
