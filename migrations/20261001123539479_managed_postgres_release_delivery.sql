-- +goose Up
-- +goose StatementBegin
-- Previously staged migration credentials may be present in captured memory.
-- Force fresh environments when the complete fleet adopts release-only delivery.
UPDATE snapshots SET stale = true
WHERE NOT stale AND deployment_id IN (
 SELECT d.id FROM deployments d JOIN managed_postgres_bindings b ON b.app_id = d.app_id
 WHERE b.access = 'migration' AND b.state <> 'deleted'
);
INSERT INTO app_runtime_config_changes (app_id, changed_at)
SELECT DISTINCT app_id, now() FROM managed_postgres_bindings
WHERE access = 'migration' AND state <> 'deleted'
ON CONFLICT (app_id) DO UPDATE SET changed_at = EXCLUDED.changed_at;
-- Resume rotations staged by an older binary without depending on a serving
-- wake. The retirement claim still waits for restoring/running tasks to drain.
UPDATE managed_postgres_bindings SET rotation_cleanup_ready = true, retry_at = now(), updated_at = now()
WHERE access = 'migration' AND rotation_previous_generation IS NOT NULL AND state IN ('ready','retiring');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Snapshot invalidation and a completed rotation cannot safely be reversed.
SELECT 1;
-- +goose StatementEnd
