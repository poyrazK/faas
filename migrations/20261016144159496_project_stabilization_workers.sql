-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE VIEW project_worker_canary_targets AS
SELECT DISTINCT a.id AS app_id,m.deployment_id FROM project_release_canaries c
JOIN project_submissions s ON s.account_id=c.account_id AND s.attempt_id=c.attempt_id
JOIN apps a ON a.id=c.worker_app_id AND a.project_id=c.project_id AND a.account_id=c.account_id AND a.status='active'
JOIN queue_bindings b ON b.id=(s.payload#>>'{apply,safe_release,worker,binding_id}')::uuid
 AND b.app_id=a.id AND b.account_id=a.account_id AND b.deployment_scope='production'
 AND b.mode='push' AND b.workload_class='worker' AND b.enabled AND b.retired_at IS NULL
JOIN project_release_sets r ON r.id IN ((c.policy->>'baseline_release_id')::uuid,(c.policy->>'candidate_release_id')::uuid)
 AND r.account_id=c.account_id AND r.project_id=c.project_id AND r.environment_slug='production'
 AND (r.active OR r.expires_at>clock_timestamp())
JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id
JOIN deployments d ON d.id=m.deployment_id AND d.app_id=a.id AND d.scope='production' AND d.status='live'
WHERE (c.status='running' OR c.status='complete' AND c.policy->>'stabilizing'='true' AND s.payload#>>'{apply,safe_release,verification,stabilization,status}'='monitoring') AND (c.policy->>'deadline')::timestamptz>clock_timestamp()
AND EXISTS(SELECT 1 FROM project_release_sets active WHERE active.project_id=c.project_id AND active.environment_slug='production'
 AND active.active AND active.id=CASE WHEN (c.policy->>'percent')::integer<100 THEN (c.policy->>'baseline_release_id')::uuid ELSE (c.policy->>'candidate_release_id')::uuid END);

-- +goose Down
CREATE OR REPLACE VIEW project_worker_canary_targets AS
SELECT DISTINCT a.id AS app_id,m.deployment_id FROM project_release_canaries c
JOIN project_submissions s ON s.account_id=c.account_id AND s.attempt_id=c.attempt_id
JOIN apps a ON a.id=c.worker_app_id AND a.project_id=c.project_id AND a.account_id=c.account_id AND a.status='active'
JOIN queue_bindings b ON b.id=(s.payload#>>'{apply,safe_release,worker,binding_id}')::uuid
 AND b.app_id=a.id AND b.account_id=a.account_id AND b.deployment_scope='production'
 AND b.mode='push' AND b.workload_class='worker' AND b.enabled AND b.retired_at IS NULL
JOIN project_release_sets r ON r.id IN ((c.policy->>'baseline_release_id')::uuid,(c.policy->>'candidate_release_id')::uuid)
 AND r.account_id=c.account_id AND r.project_id=c.project_id AND r.environment_slug='production'
 AND (r.active OR r.expires_at>clock_timestamp())
JOIN project_release_members m ON m.release_id=r.id AND m.app_id=a.id
JOIN deployments d ON d.id=m.deployment_id AND d.app_id=a.id AND d.scope='production' AND d.status='live'
WHERE c.status='running' AND (c.policy->>'deadline')::timestamptz>clock_timestamp()
AND EXISTS(SELECT 1 FROM project_release_sets active WHERE active.project_id=c.project_id AND active.environment_slug='production'
 AND active.active AND active.id=CASE WHEN (c.policy->>'percent')::integer<100 THEN (c.policy->>'baseline_release_id')::uuid ELSE (c.policy->>'candidate_release_id')::uuid END);
