-- +goose Up
ALTER TABLE scenario_test_action_policies
 ADD COLUMN managed_work_plan jsonb,
 ADD COLUMN managed_work_closing boolean NOT NULL DEFAULT false,
 ADD COLUMN managed_work_failed boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT scenario_managed_work_plan_check CHECK (managed_work_plan IS NULL OR
  (jsonb_typeof(managed_work_plan)='object' AND managed_work_plan->>'version'='1'
   AND octet_length(managed_work_plan::text)<=16384 AND simulation_plan IS NOT NULL AND network_mode='contained'));

CREATE TABLE scenario_managed_work (
 invocation_id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
 document jsonb NOT NULL CHECK (jsonb_typeof(document)='object' AND octet_length(document::text)<=32768),
 payload jsonb NOT NULL CHECK (octet_length(payload::text)<=8192)
);
CREATE INDEX scenario_managed_work_run ON scenario_managed_work(account_id,run_id);

-- +goose StatementBegin
CREATE FUNCTION scenario_managed_work_policy_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.managed_work_plan IS DISTINCT FROM OLD.managed_work_plan
  OR (OLD.managed_work_closing AND NOT NEW.managed_work_closing)
  OR (OLD.managed_work_failed AND NOT NEW.managed_work_failed) THEN
  RAISE EXCEPTION 'managed rehearsal policy is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_managed_work_policy_fence BEFORE UPDATE ON scenario_test_action_policies
 FOR EACH ROW EXECUTE FUNCTION scenario_managed_work_policy_fence();

-- +goose StatementBegin
CREATE FUNCTION scenario_managed_work_capture() RETURNS trigger LANGUAGE plpgsql AS $$
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
  IF NEW.state='dispatching' AND (OLD.state<>'dispatching' OR NEW.attempts<>OLD.attempts) THEN
   SELECT jsonb_build_object('number',NEW.attempts,'instance_id',i.id,'deployment_id',i.deployment_id,'outcome','running') INTO item
    FROM instances i WHERE i.id=NULLIF(NEW.instance_id,'')::uuid AND i.app_id=p.app_id AND i.deployment_id=dep;
   IF item IS NULL OR jsonb_array_length(attempts)>=32 OR NEW.attempts<>jsonb_array_length(attempts)+1 THEN invalid:=true;
   ELSE attempts:=attempts||jsonb_build_array(item); END IF;
  END IF;
  doc:=jsonb_set(jsonb_set(doc,'{state}',to_jsonb(NEW.state)),'{attempts}',attempts);
  UPDATE scenario_managed_work SET document=doc WHERE invocation_id=OLD.id;
 END IF;
 IF invalid THEN UPDATE scenario_test_action_policies SET managed_work_failed=true WHERE account_id=p.account_id AND run_id=p.run_id; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_managed_work_capture BEFORE INSERT OR UPDATE OR DELETE ON invocations
 FOR EACH ROW EXECUTE FUNCTION scenario_managed_work_capture();

-- +goose StatementBegin
CREATE FUNCTION scenario_managed_work_journal_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF pg_trigger_depth()<2 THEN
  RAISE EXCEPTION 'managed work journal is owned by invocation transitions' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND ((NEW.invocation_id,NEW.app_id,NEW.account_id,NEW.run_id,NEW.payload)
   IS DISTINCT FROM (OLD.invocation_id,OLD.app_id,OLD.account_id,OLD.run_id,OLD.payload)
   OR (NEW.document - ARRAY['state','attempts']) IS DISTINCT FROM (OLD.document - ARRAY['state','attempts'])) THEN
  RAISE EXCEPTION 'managed work journal intent is immutable' USING ERRCODE='23514';
 END IF;
 RETURN COALESCE(NEW,OLD);
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_managed_work_journal_fence BEFORE INSERT OR UPDATE OR DELETE ON scenario_managed_work
 FOR EACH ROW EXECUTE FUNCTION scenario_managed_work_journal_fence();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE managed_work_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove managed-work fencing while enrollment remains';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_managed_work_capture ON invocations;
DROP FUNCTION scenario_managed_work_capture();
DROP TABLE scenario_managed_work;
DROP FUNCTION scenario_managed_work_journal_fence();
DROP TRIGGER scenario_managed_work_policy_fence ON scenario_test_action_policies;
DROP FUNCTION scenario_managed_work_policy_fence();
ALTER TABLE scenario_test_action_policies DROP CONSTRAINT scenario_managed_work_plan_check,
 DROP COLUMN managed_work_plan,DROP COLUMN managed_work_closing,DROP COLUMN managed_work_failed;
