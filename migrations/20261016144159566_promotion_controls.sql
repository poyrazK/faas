-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_environment_promotion_health'::regclass AND conname='project_environment_promotion_health_phase_check' AND strpos(pg_get_constraintdef(oid), '''restoring''') > 0) THEN
  ALTER TABLE project_environment_promotion_health DROP CONSTRAINT IF EXISTS project_environment_promotion_health_phase_check;
  ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('migrating','preflight','cutover','verifying','finalizing','rolling_back','restoring','aborted','succeeded','failed','conflicted','rollback_failed'));
 END IF;
END $$;
-- +goose StatementEnd
CREATE TABLE IF NOT EXISTS project_environment_promotion_controls (
 promotion_id uuid NOT NULL REFERENCES project_environment_promotions(id) ON DELETE CASCADE,
 request_id text NOT NULL CHECK (request_id ~ '^[a-f0-9]{32}$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object' AND payload#>>'{request,request_id}'=request_id AND (payload#>>'{request,expected_generation}')::bigint>0 AND payload->>'action' IN ('abort','recover') AND payload->>'status' IN ('pending','aborted','restored','blocked') AND payload ?& ARRAY['request','action','status','operator_account_id','requested_at','deadline'] AND octet_length(payload::text)<=4096),
 PRIMARY KEY(promotion_id,request_id)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_control() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM project_environment_promotions WHERE id=OLD.promotion_id) THEN
   RAISE EXCEPTION 'promotion control receipts follow promotion lifetime' USING ERRCODE='23514';
  END IF;
  RETURN OLD;
 END IF;
 IF NEW.promotion_id<>OLD.promotion_id OR NEW.request_id<>OLD.request_id OR
  (NEW.payload-'status'-'reason'-'resolved_at') IS DISTINCT FROM (OLD.payload-'status'-'reason'-'resolved_at') OR
  (OLD.payload->>'status'<>'pending' AND NEW.payload IS DISTINCT FROM OLD.payload) THEN
  RAISE EXCEPTION 'promotion control identity and terminal outcome are immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_promotion_control BEFORE UPDATE OR DELETE ON project_environment_promotion_controls FOR EACH ROW EXECUTE FUNCTION protect_promotion_control();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_control() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.payload ? 'control' AND NOT NEW.payload ? 'control' OR
  (NEW.payload ? 'control' AND NOT EXISTS(SELECT 1 FROM project_environment_promotion_controls c WHERE c.promotion_id=NEW.promotion_id AND c.request_id=NEW.payload#>>'{control,request,request_id}' AND c.payload=NEW.payload->'control')) THEN
  RAISE EXCEPTION 'promotion health must preserve the durable operator control' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_health_control_gate BEFORE UPDATE ON project_environment_promotion_health FOR EACH ROW EXECUTE FUNCTION protect_promotion_health_control();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE health_phase text;
BEGIN
 SELECT phase INTO health_phase FROM project_environment_promotion_health WHERE promotion_id=OLD.id;
 IF health_phase IS NOT NULL AND (
  (NEW.status='running' AND OLD.status<>'running' AND NOT (OLD.status='failed' AND (health_phase='preflight' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries WHERE promotion_id=OLD.id AND status='resumed') OR health_phase='restoring' AND EXISTS(SELECT 1 FROM project_environment_promotion_controls WHERE promotion_id=OLD.id AND payload->>'status'='pending')))) OR
  (NEW.status='succeeded' AND health_phase<>'succeeded') OR
  (NEW.status='failed' AND health_phase NOT IN ('preflight','aborted','failed','conflicted','rollback_failed'))
 ) THEN RAISE EXCEPTION 'promotion status is owned by destination health' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_promotion_control_publication() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE h record; publication_token text;
BEGIN
 SELECT generation,phase,payload INTO h FROM project_environment_promotion_health WHERE promotion_id=NEW.id;
 IF FOUND AND h.payload ? 'control' THEN
  IF NEW.target_release_set_id IS DISTINCT FROM OLD.target_release_set_id THEN
   RAISE EXCEPTION 'operator-controlled promotion cannot cut over' USING ERRCODE='23514',CONSTRAINT='promotion_control_publication_fenced';
  END IF;
  IF NEW.restored_target_release_set_id IS DISTINCT FROM OLD.restored_target_release_set_id THEN
   publication_token := NEW.id::text || ':' || h.generation::text || ':' || (h.payload#>>'{control,request,request_id}');
   IF h.phase<>'rolling_back' OR h.payload#>>'{control,status}'<>'pending' OR
      (h.payload#>>'{control,deadline}')::timestamptz<=clock_timestamp() OR
      current_setting('faas.promotion_control_publication',true) IS DISTINCT FROM publication_token THEN
    RAISE EXCEPTION 'operator restoration requires the guarded publication transaction' USING ERRCODE='23514',CONSTRAINT='promotion_control_publication_fenced';
   END IF;
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER promotion_control_publication_gate BEFORE UPDATE OF target_release_set_id,restored_target_release_set_id ON project_environment_promotions FOR EACH ROW EXECUTE FUNCTION guard_promotion_control_publication();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_promotion_controls) THEN RAISE EXCEPTION 'cannot discard durable promotion controls'; END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER promotion_control_publication_gate ON project_environment_promotions;
DROP FUNCTION guard_promotion_control_publication();
DROP TRIGGER promotion_health_control_gate ON project_environment_promotion_health;
DROP FUNCTION protect_promotion_health_control();
DROP TABLE project_environment_promotion_controls;
DROP FUNCTION protect_promotion_control();
ALTER TABLE project_environment_promotion_health DROP CONSTRAINT project_environment_promotion_health_phase_check;
ALTER TABLE project_environment_promotion_health ADD CONSTRAINT project_environment_promotion_health_phase_check CHECK (phase IN ('migrating','preflight','cutover','verifying','finalizing','rolling_back','succeeded','failed','conflicted','rollback_failed'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_promotion_health_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE health_phase text;
BEGIN
 SELECT phase INTO health_phase FROM project_environment_promotion_health WHERE promotion_id=OLD.id;
 IF health_phase IS NOT NULL AND (
  (NEW.status='running' AND OLD.status<>'running' AND NOT (OLD.status='failed' AND health_phase='preflight' AND EXISTS(SELECT 1 FROM project_environment_promotion_migration_recoveries WHERE promotion_id=OLD.id AND status='resumed'))) OR
  (NEW.status='succeeded' AND health_phase<>'succeeded') OR
  (NEW.status='failed' AND health_phase NOT IN ('preflight','failed','conflicted','rollback_failed'))
 ) THEN RAISE EXCEPTION 'promotion status is owned by destination health' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
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
