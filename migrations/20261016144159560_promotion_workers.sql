-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A derived hold, scoped to the promotion's frozen worker policy. It authorizes
-- startup only; queue admission remains owned by the active release graph.
CREATE OR REPLACE VIEW project_promotion_worker_targets AS
 SELECT p.id promotion_id,p.project_id,p.account_id,pin.app_id,pin.deployment_id
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 JOIN project_environment_promotion_workload_specs pin ON pin.promotion_id=p.id AND pin.app_id::text=h.payload#>>'{policy,worker,app_id}'
 JOIN deployments d ON d.id=pin.deployment_id AND d.app_id=pin.app_id AND d.scope='production'
 WHERE p.status='running' AND p.to_environment='production' AND p.target_release_set_id IS NULL
 AND h.phase IN ('preflight','cutover') AND (h.payload->>'deadline')::timestamptz>now()
 AND d.status NOT IN ('failed','cancelled','superseded')
 UNION
 SELECT p.id,p.project_id,p.account_id,d.app_id,d.id
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 JOIN deployments d ON d.id::text=h.payload#>>'{policy,worker,previous_deployment_id}' AND d.app_id::text=h.payload#>>'{policy,worker,app_id}' AND d.scope='production'
 WHERE p.target_release_set_id IS NOT NULL AND p.restored_target_release_set_id IS NULL
 AND (h.phase='rolling_back' OR p.rollback_status='rolling_back')
 AND (CASE WHEN p.rollback_status='rolling_back' THEN coalesce(p.rollback_started_at,(h.payload->>'deadline')::timestamptz) ELSE (h.payload->>'deadline')::timestamptz END)+interval '10 minutes'>now()
 AND d.status NOT IN ('failed','cancelled');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_promotion_worker_targets) THEN RAISE EXCEPTION 'cannot discard active promotion worker holds'; END IF;
END $$;
-- +goose StatementEnd
DROP VIEW project_promotion_worker_targets;
