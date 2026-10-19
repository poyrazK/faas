-- migration-compat: expand
-- filename: 20261016144159591_promotion_worker_sets.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='promotion_worker_set_shape') THEN
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT promotion_worker_set_shape CHECK (
 NOT (payload->'policy' ? 'workers') OR
 (jsonb_typeof(payload#>'{policy,workers}')='array' AND jsonb_array_length(payload#>'{policy,workers}') BETWEEN 2 AND 8 AND NOT (payload->'policy' ? 'worker')));
 END IF;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW project_promotion_worker_targets AS
 SELECT p.id promotion_id,p.project_id,p.account_id,pin.app_id,pin.deployment_id
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(h.payload#>'{policy,workers}')='array' THEN h.payload#>'{policy,workers}' ELSE jsonb_build_array(h.payload#>'{policy,worker}') END) worker(policy)
 JOIN project_environment_promotion_workload_specs pin ON pin.promotion_id=p.id AND pin.app_id::text=(worker.policy->>'app_id')
 JOIN deployments d ON d.id=pin.deployment_id AND d.app_id=pin.app_id AND d.scope='production'
 WHERE p.status='running' AND p.to_environment='production' AND p.target_release_set_id IS NULL
 AND h.phase IN ('preflight','cutover') AND (h.payload->>'deadline')::timestamptz>now()
 AND d.status NOT IN ('failed','cancelled','superseded')
 UNION
 SELECT p.id,p.project_id,p.account_id,d.app_id,d.id
 FROM project_environment_promotions p JOIN project_environment_promotion_health h ON h.promotion_id=p.id
 CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(h.payload#>'{policy,workers}')='array' THEN h.payload#>'{policy,workers}' ELSE jsonb_build_array(h.payload#>'{policy,worker}') END) worker(policy)
 JOIN deployments d ON d.id::text=(worker.policy->>'previous_deployment_id') AND d.app_id::text=(worker.policy->>'app_id') AND d.scope='production'
 WHERE p.target_release_set_id IS NOT NULL AND p.restored_target_release_set_id IS NULL
 AND (CASE WHEN h.payload ? 'control' THEN EXISTS(SELECT 1 FROM project_release_sets baseline WHERE baseline.id=p.previous_target_release_set_id AND (baseline.active OR baseline.expires_at>now())) AND h.payload#>>'{control,status}'='pending' AND h.phase IN ('restoring','rolling_back') AND (h.payload#>>'{control,deadline}')::timestamptz>now()
 ELSE (h.phase='rolling_back' OR p.rollback_status='rolling_back') AND (CASE WHEN p.rollback_status='rolling_back' THEN coalesce(p.rollback_started_at,(h.payload->>'deadline')::timestamptz) ELSE (h.payload->>'deadline')::timestamptz END)+interval '10 minutes'>now() END)
 AND d.status NOT IN ('failed','cancelled');
-- An older controller cannot publish a multi-worker operation without all
-- per-worker startup guards in this same transaction and health generation.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_promotion_worker_set_publication() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE h record; restoring boolean;
BEGIN
 SELECT generation,payload INTO h FROM project_environment_promotion_health WHERE promotion_id=NEW.id;
 IF FOUND AND h.payload->'policy' ? 'workers' AND
  (NEW.target_release_set_id IS DISTINCT FROM OLD.target_release_set_id OR NEW.restored_target_release_set_id IS DISTINCT FROM OLD.restored_target_release_set_id) THEN
  restoring := NEW.restored_target_release_set_id IS DISTINCT FROM OLD.restored_target_release_set_id;
  IF current_setting('faas.promotion_worker_set_guard',true) IS DISTINCT FROM NEW.id::text||':'||h.generation::text||':'||restoring::text THEN
   RAISE EXCEPTION 'worker set publication requires every current startup guard' USING ERRCODE='23514',CONSTRAINT='promotion_worker_set_publication_fenced';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_worker_set_publication_gate BEFORE UPDATE OF target_release_set_id,restored_target_release_set_id ON project_environment_promotions FOR EACH ROW EXECUTE FUNCTION guard_promotion_worker_set_publication();
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_health WHERE payload->'policy' ? 'workers') THEN RAISE EXCEPTION 'cannot discard durable worker sets'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER promotion_worker_set_publication_gate ON project_environment_promotions;
DROP FUNCTION guard_promotion_worker_set_publication();
ALTER TABLE project_environment_promotion_health DROP CONSTRAINT promotion_worker_set_shape;
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
 AND (CASE WHEN h.payload ? 'control' THEN EXISTS(SELECT 1 FROM project_release_sets baseline WHERE baseline.id=p.previous_target_release_set_id AND (baseline.active OR baseline.expires_at>now())) AND h.payload#>>'{control,status}'='pending' AND h.phase IN ('restoring','rolling_back') AND (h.payload#>>'{control,deadline}')::timestamptz>now()
 ELSE (h.phase='rolling_back' OR p.rollback_status='rolling_back') AND (CASE WHEN p.rollback_status='rolling_back' THEN coalesce(p.rollback_started_at,(h.payload->>'deadline')::timestamptz) ELSE (h.payload->>'deadline')::timestamptz END)+interval '10 minutes'>now() END)
 AND d.status NOT IN ('failed','cancelled');
