-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS project_worker_release_pins_app_target ON project_worker_release_pins(app_id,deployment_id);
CREATE OR REPLACE VIEW project_worker_pinned_targets AS
SELECT DISTINCT p.app_id,p.deployment_id FROM project_worker_release_pins p
JOIN invocations root ON root.id=p.root_invocation_id AND root.app_id=p.app_id AND root.queue_binding_id=p.binding_id
JOIN apps a ON a.id=p.app_id AND a.account_id=root.account_id AND a.status='active'
JOIN deployments d ON d.id=p.deployment_id AND d.app_id=a.id AND d.status='live' AND d.scope='production'
JOIN project_release_sets r ON r.id=p.release_id AND r.project_id=a.project_id AND r.account_id=a.account_id
 AND r.environment_slug='production' AND (r.active OR r.expires_at>clock_timestamp())
JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id AND m.deployment_id=d.id
JOIN queue_bindings b ON b.id=p.binding_id AND b.account_id=a.account_id AND b.app_id=a.id
 AND b.deployment_scope='production' AND b.mode='push' AND b.workload_class='worker' AND b.enabled AND b.retired_at IS NULL
WHERE EXISTS(SELECT 1 FROM invocations i WHERE i.account_id=a.account_id AND i.app_id=a.id
 AND i.queue_binding_id=b.id AND i.deployment_scope='production' AND i.environment_id IS NULL
 AND i.state IN ('pending','dispatching') AND (i.id=root.id OR i.replay_root_invocation_id=root.id));

-- +goose Down
DROP VIEW project_worker_pinned_targets;
DROP INDEX project_worker_release_pins_app_target;
