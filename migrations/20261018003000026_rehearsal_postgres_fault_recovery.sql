-- filename: 20261018003000026_rehearsal_postgres_fault_recovery.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS scenario_postgres_faults (
 id uuid PRIMARY KEY,
 resource_id uuid NOT NULL UNIQUE REFERENCES scenario_postgres_fixtures(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK(run_id ~ '^[a-f0-9]{32}$'),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 owner_id uuid NOT NULL,
 phase text NOT NULL CHECK(phase IN ('requested','cutting','active','recovering','recovered','failed')),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND
  (receipt->>'id'=id::text AND receipt->>'resource_id'=resource_id::text AND receipt->>'account_id'=account_id::text
   AND receipt->>'app_id'=app_id::text AND receipt->>'owner_id'=owner_id::text AND receipt->>'run_id'=run_id AND receipt->>'state'=phase) IS TRUE),
 UNIQUE(account_id,run_id)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_postgres_fault_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE; f scenario_postgres_fixtures%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM scenario_postgres_fixtures WHERE id=OLD.resource_id AND status NOT IN ('complete','deleted'))
   THEN RAISE EXCEPTION 'PostgreSQL fault recovery is retained until resource cleanup' USING ERRCODE='23514'; END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.resource_id,NEW.account_id,NEW.run_id,NEW.app_id,NEW.owner_id) IS DISTINCT FROM
     (OLD.id,OLD.resource_id,OLD.account_id,OLD.run_id,OLD.app_id,OLD.owner_id)
   OR (NEW.receipt - ARRAY['state','activated','recovered','closed_connections','outage_ms','recovery_ms','error','restore_at','deadline_at']) IS DISTINCT FROM
      (OLD.receipt - ARRAY['state','activated','recovered','closed_connections','outage_ms','recovery_ms','error','restore_at','deadline_at'])
   OR NOT ((OLD.phase='requested' AND NEW.phase IN ('cutting','recovering')) OR
    (OLD.phase='cutting' AND NEW.phase IN ('active','recovering')) OR (OLD.phase='active' AND NEW.phase='recovering') OR
    (OLD.phase='recovering' AND NEW.phase IN ('recovered','failed')) OR (OLD.phase='failed' AND NEW.phase='recovering') OR
    (OLD.phase='recovered' AND NEW.phase='failed'))
   OR (OLD.receipt->>'error' IS NOT NULL AND NEW.receipt->>'error' IS DISTINCT FROM OLD.receipt->>'error')
   OR (OLD.receipt->>'activated'='true' AND NEW.receipt->>'activated' IS DISTINCT FROM 'true')
   OR (OLD.receipt->>'recovered'='true' AND NEW.receipt->>'recovered' IS DISTINCT FROM 'true')
   OR ((OLD.receipt->>'restore_at')::timestamptz > '0001-01-01 UTC'::timestamptz AND NEW.receipt->>'restore_at' IS DISTINCT FROM OLD.receipt->>'restore_at')
   OR (NEW.receipt->>'deadline_at')::timestamptz < (OLD.receipt->>'deadline_at')::timestamptz
   THEN RAISE EXCEPTION 'PostgreSQL fault identity or phase is immutable' USING ERRCODE='23514'; END IF;
  IF NEW.phase='recovered' AND NOT ((NEW.receipt->>'activated'='true' AND NEW.receipt->>'recovered'='true'
   AND NOT NEW.receipt ? 'error' AND (NEW.receipt->>'outage_ms')::bigint >= (NEW.receipt->'spec'->>'unavailable_ms')::bigint
   AND (NEW.receipt->>'recovery_ms')::bigint>=0) IS TRUE)
   THEN RAISE EXCEPTION 'PostgreSQL recovery proof is missing' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||NEW.account_id::text||':'||NEW.run_id,0));
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 SELECT * INTO f FROM scenario_postgres_fixtures WHERE id=NEW.resource_id;
 IF NEW.phase<>'requested' OR p.managed_work_plan->'postgres_fault' IS DISTINCT FROM NEW.receipt->'spec'
  OR NEW.receipt->>'activated' IS DISTINCT FROM 'false' OR NEW.receipt->>'recovered' IS DISTINCT FROM 'false'
  OR NEW.receipt->>'closed_connections' IS DISTINCT FROM '0' OR NEW.receipt->>'outage_ms' IS DISTINCT FROM '0'
  OR NEW.receipt->>'recovery_ms' IS DISTINCT FROM '0' OR NEW.receipt ? 'error'
  OR NOT coalesce(NEW.receipt->>'plan_sha256' ~ '^[a-f0-9]{64}$',false)
  OR p.account_id IS DISTINCT FROM NEW.account_id OR p.run_id IS DISTINCT FROM NEW.run_id
  OR p.sealed_at IS NOT NULL OR p.recorder_closing OR p.managed_work_failed
  OR f.owner_id IS DISTINCT FROM NEW.owner_id OR f.account_id IS DISTINCT FROM NEW.account_id OR f.run_id IS DISTINCT FROM NEW.run_id
  OR f.backend_sha256 IS DISTINCT FROM NEW.receipt->>'backend_sha256' OR f.status IS DISTINCT FROM 'ready' OR f.expires_at<=clock_timestamp()
  OR NOT EXISTS(SELECT 1 FROM scenario_recorder_sessions r JOIN scenario_recorder_requests q ON q.session_id=r.id
   JOIN instances i ON i.id=r.instance_id JOIN invocations v ON nullif(v.instance_id,'')::uuid=i.id AND v.app_id=i.app_id
   WHERE r.id=(NEW.receipt->>'recorder_session_id')::uuid AND r.app_id=NEW.app_id AND r.instance_id=(NEW.receipt->>'instance_id')::uuid
    AND r.deployment_id=(NEW.receipt->>'deployment_id')::uuid AND r.status='recording' AND q.status='admitted'
    AND q.attempt_id=(NEW.receipt->>'checkpoint_attempt_id')::uuid AND i.state='running'
    AND v.id=(NEW.receipt->>'invocation_id')::uuid AND v.state='dispatching' AND v.source='async_invoke'
    AND v.attempts=1 AND v.replay_generation=0 AND v.lease_expires_at>clock_timestamp())
  THEN RAISE EXCEPTION 'PostgreSQL fault checkpoint is not owned by its original dispatch' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER scenario_postgres_fault_guard BEFORE INSERT OR UPDATE OR DELETE ON scenario_postgres_faults FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fault_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_postgres_fault_scope() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE;
BEGIN
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF p.managed_work_plan->'postgres_fault' IS NULL THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
 IF TG_OP='INSERT' THEN
  IF p.workload_name IS DISTINCT FROM p.managed_work_plan->'postgres_fault'->>'workload' OR NEW.source<>'async_invoke'
   OR EXISTS(SELECT 1 FROM invocations v JOIN scenario_test_action_policies w ON w.app_id=v.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id)
   THEN RAISE EXCEPTION 'PostgreSQL fault supports one managed HTTP job' USING ERRCODE='23514'; END IF;
 ELSIF NEW.state='dispatching' AND (NEW.state,NEW.attempts,NEW.instance_id) IS DISTINCT FROM (OLD.state,OLD.attempts,OLD.instance_id) THEN
  IF EXISTS(SELECT 1 FROM scenario_postgres_faults f WHERE f.account_id=p.account_id AND f.run_id=p.run_id AND
   (f.phase IN ('requested','cutting') OR (nullif(NEW.instance_id,'') IS NOT NULL AND NEW.instance_id IS DISTINCT FROM f.receipt->>'instance_id')))
   THEN RAISE EXCEPTION 'PostgreSQL recovery retains its original worker dispatch' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER scenario_postgres_fault_scope BEFORE INSERT OR UPDATE OF state,attempts,instance_id ON invocations FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fault_scope();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_postgres_fault_policy_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE f jsonb; p jsonb;
BEGIN
 f:=NEW.managed_work_plan->'postgres_fault'; p:=NEW.managed_work_plan;
 IF f IS NULL THEN RETURN NEW; END IF;
 IF NOT ((f->>'workload' ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'
  AND f->>'checkpoint' ~ '^[a-z][a-z0-9-]{0,62}$'
  AND (f->>'unavailable_ms')::bigint BETWEEN 100 AND 30000
  AND (f->>'recover_within_ms')::bigint BETWEEN 1 AND 120000
  AND NEW.suspend_resume_spec IS NULL AND NEW.cold_start_spec IS NULL AND NOT p ? 'worker_termination'
  AND jsonb_array_length(p->'participants')=2
  AND (SELECT count(*) FROM jsonb_array_elements(p->'participants') v WHERE coalesce((v->>'async')::boolean,false))=1
  AND EXISTS(SELECT 1 FROM jsonb_array_elements(p->'participants') v WHERE v->>'workload'=f->>'workload' AND v->>'postgres'='read_write' AND v->>'async'='true')
  AND EXISTS(SELECT 1 FROM jsonb_array_elements(p->'participants') v WHERE v->>'workload'=p->>'primary' AND v->>'postgres'='read_write' AND NOT coalesce((v->>'async')::boolean,false))
  AND EXISTS(SELECT 1 FROM jsonb_array_elements(NEW.simulation_plan->'fixtures') v
   WHERE v->>'workload'=f->>'workload' AND v->>'name'=f->>'checkpoint' AND v->>'integration'='checkpoint'
   AND v->>'adapter'='http' AND v->>'method'='POST' AND jsonb_array_length(v->'responses')=1
   AND v->'responses'->0->>'status'='200' AND coalesce(v->'responses'->0->>'fault','')=''
   AND coalesce(v->'responses'->0->>'delay_ms','0')='0' AND coalesce(v->'responses'->0->>'retry_after_seconds','0')='0')) IS TRUE)
  THEN RAISE EXCEPTION 'unsupported PostgreSQL fault enrollment' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER scenario_postgres_fault_policy_guard BEFORE INSERT OR UPDATE ON scenario_test_action_policies FOR EACH ROW EXECUTE FUNCTION scenario_postgres_fault_policy_guard();

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_postgres_faults) OR EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE managed_work_plan ? 'postgres_fault')
 THEN RAISE EXCEPTION 'PostgreSQL fault enrollment or recovery evidence is retained'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_postgres_fault_scope ON invocations;
DROP FUNCTION scenario_postgres_fault_scope();
DROP TRIGGER scenario_postgres_fault_policy_guard ON scenario_test_action_policies;
DROP FUNCTION scenario_postgres_fault_policy_guard();
DROP TRIGGER scenario_postgres_fault_guard ON scenario_postgres_faults;
DROP FUNCTION scenario_postgres_fault_guard();
DROP TABLE scenario_postgres_faults;
