-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_worker_faults') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_worker_faults') AND tgname='scenario_worker_fault_guard' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_worker_fault_guard()') AND md5(prosrc)=ANY(ARRAY['05ed5d917d0a67c0fcf5708b0003ef9a']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['b8810e6de73ef5c1c55a3a009e64d2b1','71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_managed_work_capture()') AND md5(prosrc)=ANY(ARRAY['75c916d3789a6685eab2331bc3d37a98']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_worker_faults') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_worker_faults') AND tgname='scenario_worker_fault_guard' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_worker_fault_guard()') AND md5(prosrc)=ANY(ARRAY['05ed5d917d0a67c0fcf5708b0003ef9a']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['b8810e6de73ef5c1c55a3a009e64d2b1','71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_managed_work_capture()') AND md5(prosrc)=ANY(ARRAY['75c916d3789a6685eab2331bc3d37a98']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 21; inspect recovery before continuing';
  END IF;
  IF to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['0dd3ae13aa0c5e0a6a2eab91961f27a7','b8810e6de73ef5c1c55a3a009e64d2b1','71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_rehearsal_execution_active';
  END IF;
  IF to_regprocedure('scenario_managed_work_capture()') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_managed_work_capture()') AND md5(prosrc)=ANY(ARRAY['c7012f901dd9db795461860a35bbba1a','75c916d3789a6685eab2331bc3d37a98']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_managed_work_capture';
  END IF;
  IF to_regprocedure('scenario_recorder_fence()') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['fea9d2738dcb6849294acd8fc0329c6a','f6368b62b0f1062b37392c3a790a1eec','63acbe9923cf5e86a31d3853032568b6','f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_recorder_fence';
  END IF;

CREATE TABLE scenario_worker_faults (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK(run_id ~ '^[0-9a-f]{32}$'),
 instance_id uuid NOT NULL,
 deployment_id uuid NOT NULL,
 invocation_id uuid NOT NULL,
 checkpoint_attempt_id uuid NOT NULL,
 recorder_session_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('armed','ready','claimed','fenced','terminated','failed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(account_id,run_id), UNIQUE(app_id)
);

CREATE FUNCTION scenario_worker_fault_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) AND EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) AND EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=OLD.app_id AND sealed_at IS NULL)
   THEN RAISE EXCEPTION 'unsealed worker fault evidence is retained' USING ERRCODE='23514'; END IF;
  RETURN OLD;
 END IF;
 IF ROW(NEW.id,NEW.account_id,NEW.app_id,NEW.run_id,NEW.instance_id,NEW.deployment_id,NEW.invocation_id,NEW.checkpoint_attempt_id,NEW.recorder_session_id,NEW.created_at)
  IS DISTINCT FROM ROW(OLD.id,OLD.account_id,OLD.app_id,OLD.run_id,OLD.instance_id,OLD.deployment_id,OLD.invocation_id,OLD.checkpoint_attempt_id,OLD.recorder_session_id,OLD.created_at)
  OR NOT(NEW.state=OLD.state OR (OLD.state<>'terminated' AND NEW.state='failed')
  OR (OLD.state='armed' AND NEW.state='ready') OR (OLD.state='ready' AND NEW.state='claimed')
  OR (OLD.state='claimed' AND NEW.state='fenced') OR (OLD.state='fenced' AND NEW.state='terminated'))
 THEN RAISE EXCEPTION 'worker fault identity or lifecycle is immutable' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_worker_fault_guard BEFORE UPDATE OR DELETE ON scenario_worker_faults FOR EACH ROW EXECUTE FUNCTION scenario_worker_fault_guard();

CREATE OR REPLACE FUNCTION scenario_rehearsal_execution_active(target_app uuid, execution_id uuid, deployment uuid DEFAULT NULL) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=target_app AND f.instance_id=execution_id AND f.state IN ('fenced','terminated','failed')) AND
 (EXISTS(SELECT 1 FROM instances i WHERE i.id=execution_id AND i.app_id=target_app
  AND (deployment IS NULL OR i.deployment_id=deployment) AND i.state IN ('waking','cold_booting','running','draining'))
 OR EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
  WHERE t.id=execution_id AND t.app_id=target_app AND t.account_id=p.account_id
  AND (deployment IS NULL OR t.deployment_id=deployment) AND t.status IN ('restoring','running')
  AND t.kind='manual' AND NOT t.command_shell AND t.max_output_bytes=65536
  AND scenario_rehearsal_command_spec(t.command)->>'run_id'=p.run_id
  AND scenario_rehearsal_command_spec(t.command)->>'deployment_id'=t.deployment_id::text
  AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL));
$$;

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

CREATE OR REPLACE FUNCTION scenario_recorder_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_app uuid; policy scenario_test_action_policies%ROWTYPE; parent scenario_recorder_sessions%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='scenario_recorder_sessions' THEN
  target_app:=NEW.app_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.app_id,NEW.instance_id,NEW.deployment_id,NEW.created_at)
       IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.instance_id,OLD.deployment_id,OLD.created_at)
       OR OLD.status<>'recording' OR NEW.status NOT IN ('closed','failed')) THEN
   RAISE EXCEPTION 'recorder identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  SELECT * INTO parent FROM scenario_recorder_sessions WHERE id=NEW.session_id;
  target_app:=parent.app_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.session_id) IS DISTINCT FROM (OLD.id,OLD.session_id)
       OR (OLD.attempt_id IS NOT NULL AND NEW.attempt_id IS DISTINCT FROM OLD.attempt_id)
       OR NOT ((OLD.status='pending' AND NEW.status IN ('admitted','failed'))
            OR (OLD.status='admitted' AND NEW.status IN ('completed','failed')))) THEN
   RAISE EXCEPTION 'recorder request identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=target_app;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:' || policy.account_id::text || ':' || policy.run_id,0));
 PERFORM id FROM apps WHERE id=target_app FOR KEY SHARE;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=target_app;
 IF policy.app_id IS NULL OR policy.simulation_plan IS NULL OR policy.sealed_at IS NOT NULL THEN
  RAISE EXCEPTION 'recorder window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (policy.recorder_closing OR
      (TG_TABLE_NAME='scenario_recorder_sessions' AND NEW.status<>'recording') OR
      (TG_TABLE_NAME='scenario_recorder_requests' AND (NEW.status<>'pending' OR parent.status<>'recording'))) THEN
  RAISE EXCEPTION 'recorder admission is closed' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='scenario_recorder_sessions' THEN
  IF TG_OP='INSERT' AND NEW.command_result IS NOT NULL THEN
   RAISE EXCEPTION 'command retirement cannot be supplied at recorder start' USING ERRCODE='23514';
  END IF;
  IF TG_OP='INSERT' AND NOT scenario_rehearsal_execution_active(NEW.app_id,NEW.instance_id,NEW.deployment_id) THEN
   RAISE EXCEPTION 'recorder execution identity is unavailable' USING ERRCODE='23514';
  END IF;
  IF NEW.command_result IS NOT NULL AND (NEW.command_result::jsonb->>'task_id' IS DISTINCT FROM NEW.instance_id::text
      OR NOT scenario_rehearsal_execution_active(NEW.app_id,NEW.instance_id,NEW.deployment_id)
      OR NOT EXISTS(SELECT 1 FROM app_tasks WHERE id=NEW.instance_id)) THEN
   RAISE EXCEPTION 'command retirement requires its active task identity' USING ERRCODE='23514';
  END IF;
  IF NEW.status='closed' AND ((NOT policy.recorder_closing AND NEW.command_result IS NULL AND NOT EXISTS
     (SELECT 1 FROM scenario_worker_faults f WHERE f.recorder_session_id=NEW.id AND f.app_id=NEW.app_id
      AND f.instance_id=NEW.instance_id AND f.deployment_id=NEW.deployment_id AND f.account_id=policy.account_id
      AND f.run_id=policy.run_id AND f.state='terminated')) OR EXISTS(SELECT 1 FROM scenario_recorder_requests
      WHERE session_id=NEW.id AND status<>'completed')) THEN
   RAISE EXCEPTION 'recorder closing requires settled request receipts' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NEW.attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM scenario_simulation_attempts e
      WHERE e.id=NEW.attempt_id AND e.app_id=target_app AND
       ((NEW.status='admitted' AND e.evidence->>'status'='pending') OR
        (NEW.status='completed' AND e.evidence->>'status'='completed') OR
        (NEW.status='failed' AND e.evidence->>'status' IN ('interrupted','unsupported')))) THEN
   RAISE EXCEPTION 'recorder acknowledgement does not match simulation evidence' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- An enrolled fault cannot be downgraded while its cleanup/evidence is pending.
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_worker_faults) THEN RAISE EXCEPTION 'worker fault receipts must be retired before downgrade'; END IF; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_rehearsal_execution_active(target_app uuid, execution_id uuid, deployment uuid DEFAULT NULL) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM instances i WHERE i.id=execution_id AND i.app_id=target_app
  AND (deployment IS NULL OR i.deployment_id=deployment) AND i.state IN ('waking','cold_booting','running','draining'))
 OR EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
  WHERE t.id=execution_id AND t.app_id=target_app AND t.account_id=p.account_id
  AND (deployment IS NULL OR t.deployment_id=deployment) AND t.status IN ('restoring','running')
  AND t.kind='manual' AND NOT t.command_shell AND t.max_output_bytes=65536
  AND scenario_rehearsal_command_spec(t.command)->>'run_id'=p.run_id
  AND scenario_rehearsal_command_spec(t.command)->>'deployment_id'=t.deployment_id::text
  AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL);
$$;
-- +goose StatementEnd
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_recorder_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_app uuid; policy scenario_test_action_policies%ROWTYPE; parent scenario_recorder_sessions%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='scenario_recorder_sessions' THEN
  target_app:=NEW.app_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.app_id,NEW.instance_id,NEW.deployment_id,NEW.created_at)
       IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.instance_id,OLD.deployment_id,OLD.created_at)
       OR OLD.status<>'recording' OR NEW.status NOT IN ('closed','failed')) THEN
   RAISE EXCEPTION 'recorder identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  SELECT * INTO parent FROM scenario_recorder_sessions WHERE id=NEW.session_id;
  target_app:=parent.app_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.session_id) IS DISTINCT FROM (OLD.id,OLD.session_id)
       OR (OLD.attempt_id IS NOT NULL AND NEW.attempt_id IS DISTINCT FROM OLD.attempt_id)
       OR NOT ((OLD.status='pending' AND NEW.status IN ('admitted','failed'))
            OR (OLD.status='admitted' AND NEW.status IN ('completed','failed')))) THEN
   RAISE EXCEPTION 'recorder request identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=target_app;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:' || policy.account_id::text || ':' || policy.run_id,0));
 PERFORM id FROM apps WHERE id=target_app FOR KEY SHARE;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=target_app;
 IF policy.app_id IS NULL OR policy.simulation_plan IS NULL OR policy.sealed_at IS NOT NULL THEN
  RAISE EXCEPTION 'recorder window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' AND (policy.recorder_closing OR
      (TG_TABLE_NAME='scenario_recorder_sessions' AND NEW.status<>'recording') OR
      (TG_TABLE_NAME='scenario_recorder_requests' AND (NEW.status<>'pending' OR parent.status<>'recording'))) THEN
  RAISE EXCEPTION 'recorder admission is closed' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='scenario_recorder_sessions' THEN
  IF TG_OP='INSERT' AND NEW.command_result IS NOT NULL THEN
   RAISE EXCEPTION 'command retirement cannot be supplied at recorder start' USING ERRCODE='23514';
  END IF;
  IF TG_OP='INSERT' AND NOT scenario_rehearsal_execution_active(NEW.app_id,NEW.instance_id,NEW.deployment_id) THEN
   RAISE EXCEPTION 'recorder execution identity is unavailable' USING ERRCODE='23514';
  END IF;
  IF NEW.command_result IS NOT NULL AND (NEW.command_result::jsonb->>'task_id' IS DISTINCT FROM NEW.instance_id::text
      OR NOT scenario_rehearsal_execution_active(NEW.app_id,NEW.instance_id,NEW.deployment_id)
      OR NOT EXISTS(SELECT 1 FROM app_tasks WHERE id=NEW.instance_id)) THEN
   RAISE EXCEPTION 'command retirement requires its active task identity' USING ERRCODE='23514';
  END IF;
  IF NEW.status='closed' AND ((NOT policy.recorder_closing AND NEW.command_result IS NULL) OR EXISTS(SELECT 1 FROM scenario_recorder_requests
      WHERE session_id=NEW.id AND status<>'completed')) THEN
   RAISE EXCEPTION 'recorder closing requires settled request receipts' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NEW.attempt_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM scenario_simulation_attempts e
      WHERE e.id=NEW.attempt_id AND e.app_id=target_app AND
       ((NEW.status='admitted' AND e.evidence->>'status'='pending') OR
        (NEW.status='completed' AND e.evidence->>'status'='completed') OR
        (NEW.status='failed' AND e.evidence->>'status' IN ('interrupted','unsupported')))) THEN
   RAISE EXCEPTION 'recorder acknowledgement does not match simulation evidence' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TABLE scenario_worker_faults;
DROP FUNCTION scenario_worker_fault_guard();
