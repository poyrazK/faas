-- filename: 20261024100845870_rehearsal_invocation_trigger_guards.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- The rehearsal triggers on invocations run for every app's work, enrolled or
-- not (ADR-20261024100845). Each change keeps the behaviour and skips work
-- that cannot matter:
-- * scenario_managed_work_capture reads the policy of NEW.app_id again on an
--   update only when the update moved the row to another app.
-- * scenario_suspend_dispatch_guard and the update half of
--   scenario_postgres_fault_scope act only on a claim, so a WHEN keeps the
--   function off every other update. The fault scope's insert half keeps its
--   trigger; its update half moves to scenario_postgres_fault_scope_dispatch,
--   which sorts right after it, so the BEFORE triggers keep their order.
-- CREATE OR REPLACE TRIGGER takes the same lock as CREATE TRIGGER, not the
-- ACCESS EXCLUSIVE lock DROP TRIGGER would.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_managed_work_capture() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE; participant jsonb; dep uuid; deployment_count integer;
 doc jsonb; attempts jsonb; item jsonb; outcome text; invalid boolean:=false;
BEGIN
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=CASE WHEN TG_OP='INSERT' THEN NEW.app_id ELSE OLD.app_id END;
 IF TG_OP='UPDATE' AND p.managed_work_plan IS NULL AND NEW.app_id IS DISTINCT FROM OLD.app_id THEN SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id; END IF;
 IF p.managed_work_plan IS NULL THEN RETURN COALESCE(NEW,OLD); END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=p.app_id;
 IF TG_OP='DELETE' THEN
  IF p.sealed_at IS NULL THEN UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id; END IF;
  RETURN OLD;
 END IF;
 IF p.sealed_at IS NOT NULL THEN
  RAISE EXCEPTION 'managed rehearsal work is sealed' USING ERRCODE='23514';
 END IF;
 SELECT value INTO participant FROM jsonb_array_elements(p.managed_work_plan->'participants') WHERE value->>'workload'=p.workload_name;
 SELECT count(*),min(id::text)::uuid INTO deployment_count,dep FROM deployments WHERE app_id=p.app_id;
 IF TG_OP='INSERT' THEN
  invalid:=p.managed_work_closing OR p.recorder_closing OR NOT COALESCE((participant->>'async')::boolean,false)
   OR NEW.source<>'async_invoke' OR NEW.state<>'pending' OR NEW.attempts<>0 OR NEW.replay_generation<>0
   OR NEW.account_id<>p.account_id OR deployment_count<>1 OR COALESCE(NEW.work_policy_name,'')<>''
   OR octet_length(COALESCE(NEW.payload,'null'::jsonb)::text)>8192 OR length(NEW.path)>2048
   OR NOT EXISTS(SELECT 1 FROM apps a WHERE a.id=p.app_id AND a.status<>'deleted' AND a.preview_pr_state='open' AND a.preview_expires_at>clock_timestamp());
  IF (SELECT count(*) FROM scenario_managed_work WHERE account_id=p.account_id AND run_id=p.run_id)>=64 THEN
   invalid:=true;
  ELSE
   doc:=jsonb_build_object('id',NEW.id,'workload',p.workload_name,'app_id',p.app_id,'deployment_id',dep,
     'method',NEW.method,'path',left(NEW.path,2048),'state',CASE WHEN invalid THEN 'cancelled' ELSE NEW.state END,'attempts','[]'::jsonb);
   INSERT INTO scenario_managed_work(invocation_id,app_id,account_id,run_id,document,payload)
    VALUES(NEW.id,p.app_id,p.account_id,p.run_id,doc,
     CASE WHEN octet_length(COALESCE(NEW.payload,'null'::jsonb)::text)<=8192 THEN COALESCE(NEW.payload,'null'::jsonb) ELSE 'null'::jsonb END);
  END IF;
  IF invalid THEN
   UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id;
   NEW.state:='cancelled'; NEW.last_error:='rehearsal: unsupported or closed managed-work admission'; NEW.completed_at:=clock_timestamp();
  END IF;
  RETURN NEW;
 END IF;
 SELECT document INTO doc FROM scenario_managed_work WHERE invocation_id=OLD.id FOR UPDATE;
 IF doc IS NULL THEN invalid:=true;
 ELSE
  attempts:=doc->'attempts';
  invalid:=(NEW.id,NEW.app_id,NEW.account_id,NEW.source,NEW.method,NEW.path,NEW.payload,NEW.replay_generation)
    IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.account_id,OLD.source,OLD.method,OLD.path,OLD.payload,OLD.replay_generation)
    OR deployment_count<>1 OR dep IS DISTINCT FROM (doc->>'deployment_id')::uuid;
  IF OLD.state='dispatching' AND (NEW.state<>'dispatching' OR NEW.attempts<>OLD.attempts) THEN
   outcome:=CASE WHEN NEW.state='completed' THEN 'succeeded'
     WHEN NEW.state='pending' AND NEW.last_error<>'dispatch lease expired; requeued' THEN 'retry'
     WHEN NEW.state='pending' THEN 'unknown' ELSE NEW.state END;
   IF jsonb_array_length(attempts)=OLD.attempts AND OLD.attempts>0 THEN
    attempts:=jsonb_set(attempts,ARRAY[(OLD.attempts-1)::text,'outcome'],to_jsonb(outcome));
   ELSE invalid:=true; END IF;
  END IF;
  IF NEW.state='dispatching' AND (OLD.state<>'dispatching' OR NEW.attempts<>OLD.attempts) AND EXISTS
   (SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=NEW.app_id AND (f.state<>'terminated' OR EXISTS
    (SELECT 1 FROM instances i WHERE i.id=f.instance_id AND i.state IN ('waking','cold_booting','running','draining'))))
  THEN RAISE EXCEPTION 'worker fault blocks dispatch until confirmed teardown' USING ERRCODE='23514'; END IF;
  IF NEW.state='dispatching' AND NULLIF(NEW.instance_id,'') IS NOT NULL AND (OLD.state<>'dispatching' OR NEW.attempts<>OLD.attempts OR jsonb_array_length(attempts)=NEW.attempts-1) THEN
   SELECT jsonb_build_object('number',NEW.attempts,'instance_id',i.id,'deployment_id',i.deployment_id,'outcome','running') INTO item
    FROM instances i WHERE i.id=NULLIF(NEW.instance_id,'')::uuid AND i.app_id=p.app_id AND i.deployment_id=dep;
   IF item IS NULL OR jsonb_array_length(attempts)>=32 OR NEW.attempts<>jsonb_array_length(attempts)+1 THEN invalid:=true;
   ELSE attempts:=attempts||jsonb_build_array(item); END IF;
  END IF;
  IF NEW.state='dispatching' AND NEW.attempts>0 AND jsonb_array_length(attempts)=NEW.attempts
   AND attempts->(NEW.attempts-1)->>'instance_id' IS DISTINCT FROM NEW.instance_id THEN invalid:=true; END IF;
  doc:=jsonb_set(jsonb_set(doc,'{state}',to_jsonb(NEW.state)),'{attempts}',attempts);
  UPDATE scenario_managed_work SET document=doc WHERE invocation_id=OLD.id;
 END IF;
 IF invalid THEN UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER scenario_suspend_dispatch_guard BEFORE UPDATE OF state, attempts, instance_id ON invocations FOR EACH ROW
    WHEN (NEW.state = 'dispatching' AND (NEW.state, NEW.attempts, NEW.instance_id) IS DISTINCT FROM (OLD.state, OLD.attempts, OLD.instance_id))
    EXECUTE FUNCTION scenario_suspend_dispatch_guard();
CREATE OR REPLACE TRIGGER scenario_postgres_fault_scope BEFORE INSERT ON invocations FOR EACH ROW
    EXECUTE FUNCTION scenario_postgres_fault_scope();
CREATE OR REPLACE TRIGGER scenario_postgres_fault_scope_dispatch BEFORE UPDATE OF state, attempts, instance_id ON invocations FOR EACH ROW
    WHEN (NEW.state = 'dispatching' AND (NEW.state, NEW.attempts, NEW.instance_id) IS DISTINCT FROM (OLD.state, OLD.attempts, OLD.instance_id))
    EXECUTE FUNCTION scenario_postgres_fault_scope();

-- +goose Down
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE TRIGGER scenario_postgres_fault_scope BEFORE INSERT OR UPDATE OF state, attempts, instance_id ON invocations FOR EACH ROW
    EXECUTE FUNCTION scenario_postgres_fault_scope();
DROP TRIGGER IF EXISTS scenario_postgres_fault_scope_dispatch ON invocations;
CREATE OR REPLACE TRIGGER scenario_suspend_dispatch_guard BEFORE UPDATE OF state, attempts, instance_id ON invocations FOR EACH ROW
    EXECUTE FUNCTION scenario_suspend_dispatch_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_managed_work_capture() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE; participant jsonb; dep uuid; deployment_count integer;
 doc jsonb; attempts jsonb; item jsonb; outcome text; invalid boolean:=false;
BEGIN
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=CASE WHEN TG_OP='INSERT' THEN NEW.app_id ELSE OLD.app_id END;
 IF TG_OP='UPDATE' AND p.managed_work_plan IS NULL THEN SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id; END IF;
 IF p.managed_work_plan IS NULL THEN RETURN COALESCE(NEW,OLD); END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=p.app_id;
 IF TG_OP='DELETE' THEN
  IF p.sealed_at IS NULL THEN UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id; END IF;
  RETURN OLD;
 END IF;
 IF p.sealed_at IS NOT NULL THEN
  RAISE EXCEPTION 'managed rehearsal work is sealed' USING ERRCODE='23514';
 END IF;
 SELECT value INTO participant FROM jsonb_array_elements(p.managed_work_plan->'participants') WHERE value->>'workload'=p.workload_name;
 SELECT count(*),min(id::text)::uuid INTO deployment_count,dep FROM deployments WHERE app_id=p.app_id;
 IF TG_OP='INSERT' THEN
  invalid:=p.managed_work_closing OR p.recorder_closing OR NOT COALESCE((participant->>'async')::boolean,false)
   OR NEW.source<>'async_invoke' OR NEW.state<>'pending' OR NEW.attempts<>0 OR NEW.replay_generation<>0
   OR NEW.account_id<>p.account_id OR deployment_count<>1 OR COALESCE(NEW.work_policy_name,'')<>''
   OR octet_length(COALESCE(NEW.payload,'null'::jsonb)::text)>8192 OR length(NEW.path)>2048
   OR NOT EXISTS(SELECT 1 FROM apps a WHERE a.id=p.app_id AND a.status<>'deleted' AND a.preview_pr_state='open' AND a.preview_expires_at>clock_timestamp());
  IF (SELECT count(*) FROM scenario_managed_work WHERE account_id=p.account_id AND run_id=p.run_id)>=64 THEN
   invalid:=true;
  ELSE
   doc:=jsonb_build_object('id',NEW.id,'workload',p.workload_name,'app_id',p.app_id,'deployment_id',dep,
     'method',NEW.method,'path',left(NEW.path,2048),'state',CASE WHEN invalid THEN 'cancelled' ELSE NEW.state END,'attempts','[]'::jsonb);
   INSERT INTO scenario_managed_work(invocation_id,app_id,account_id,run_id,document,payload)
    VALUES(NEW.id,p.app_id,p.account_id,p.run_id,doc,
     CASE WHEN octet_length(COALESCE(NEW.payload,'null'::jsonb)::text)<=8192 THEN COALESCE(NEW.payload,'null'::jsonb) ELSE 'null'::jsonb END);
  END IF;
  IF invalid THEN
   UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id;
   NEW.state:='cancelled'; NEW.last_error:='rehearsal: unsupported or closed managed-work admission'; NEW.completed_at:=clock_timestamp();
  END IF;
  RETURN NEW;
 END IF;
 SELECT document INTO doc FROM scenario_managed_work WHERE invocation_id=OLD.id FOR UPDATE;
 IF doc IS NULL THEN invalid:=true;
 ELSE
  attempts:=doc->'attempts';
  invalid:=(NEW.id,NEW.app_id,NEW.account_id,NEW.source,NEW.method,NEW.path,NEW.payload,NEW.replay_generation)
    IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.account_id,OLD.source,OLD.method,OLD.path,OLD.payload,OLD.replay_generation)
    OR deployment_count<>1 OR dep IS DISTINCT FROM (doc->>'deployment_id')::uuid;
  IF OLD.state='dispatching' AND (NEW.state<>'dispatching' OR NEW.attempts<>OLD.attempts) THEN
   outcome:=CASE WHEN NEW.state='completed' THEN 'succeeded'
     WHEN NEW.state='pending' AND NEW.last_error<>'dispatch lease expired; requeued' THEN 'retry'
     WHEN NEW.state='pending' THEN 'unknown' ELSE NEW.state END;
   IF jsonb_array_length(attempts)=OLD.attempts AND OLD.attempts>0 THEN
    attempts:=jsonb_set(attempts,ARRAY[(OLD.attempts-1)::text,'outcome'],to_jsonb(outcome));
   ELSE invalid:=true; END IF;
  END IF;
  IF NEW.state='dispatching' AND (OLD.state<>'dispatching' OR NEW.attempts<>OLD.attempts) AND EXISTS
   (SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=NEW.app_id AND (f.state<>'terminated' OR EXISTS
    (SELECT 1 FROM instances i WHERE i.id=f.instance_id AND i.state IN ('waking','cold_booting','running','draining'))))
  THEN RAISE EXCEPTION 'worker fault blocks dispatch until confirmed teardown' USING ERRCODE='23514'; END IF;
  IF NEW.state='dispatching' AND NULLIF(NEW.instance_id,'') IS NOT NULL AND (OLD.state<>'dispatching' OR NEW.attempts<>OLD.attempts OR jsonb_array_length(attempts)=NEW.attempts-1) THEN
   SELECT jsonb_build_object('number',NEW.attempts,'instance_id',i.id,'deployment_id',i.deployment_id,'outcome','running') INTO item
    FROM instances i WHERE i.id=NULLIF(NEW.instance_id,'')::uuid AND i.app_id=p.app_id AND i.deployment_id=dep;
   IF item IS NULL OR jsonb_array_length(attempts)>=32 OR NEW.attempts<>jsonb_array_length(attempts)+1 THEN invalid:=true;
   ELSE attempts:=attempts||jsonb_build_array(item); END IF;
  END IF;
  IF NEW.state='dispatching' AND NEW.attempts>0 AND jsonb_array_length(attempts)=NEW.attempts
   AND attempts->(NEW.attempts-1)->>'instance_id' IS DISTINCT FROM NEW.instance_id THEN invalid:=true; END IF;
  doc:=jsonb_set(jsonb_set(doc,'{state}',to_jsonb(NEW.state)),'{attempts}',attempts);
  UPDATE scenario_managed_work SET document=doc WHERE invocation_id=OLD.id;
 END IF;
 IF invalid THEN UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
