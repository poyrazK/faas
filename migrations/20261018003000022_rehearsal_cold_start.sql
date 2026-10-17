-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_cold_preparations') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_cold_policy_immutable' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_cold_preparations') AND tgname='scenario_cold_guard' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='cold_start_spec' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_cold_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['5260d2d6d305937d6792c5d449ca63ea']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_cold_guard()') AND md5(prosrc)=ANY(ARRAY['1d762426234838b634a8d1cc5944d6f7']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['1723aaeb805fb945bed86404d881b0c7'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_cold_preparations') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_cold_policy_immutable' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_cold_preparations') AND tgname='scenario_cold_guard' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='cold_start_spec' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_cold_policy_immutable()') AND md5(prosrc)=ANY(ARRAY['5260d2d6d305937d6792c5d449ca63ea']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_cold_guard()') AND md5(prosrc)=ANY(ARRAY['1d762426234838b634a8d1cc5944d6f7']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['1723aaeb805fb945bed86404d881b0c7'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 22; inspect recovery before continuing';
  END IF;
  IF to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_rehearsal_execution_active(uuid,uuid,uuid)') AND md5(prosrc)=ANY(ARRAY['0dd3ae13aa0c5e0a6a2eab91961f27a7','b8810e6de73ef5c1c55a3a009e64d2b1','71d203b16ce1559d2e9ad7c283ba47fa','39ee8ff29a974999ce836e3d3d7b55c2']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_rehearsal_execution_active';
  END IF;
  IF to_regprocedure('scenario_recorder_fence()') IS NOT NULL AND NOT (EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['fea9d2738dcb6849294acd8fc0329c6a','f6368b62b0f1062b37392c3a790a1eec','63acbe9923cf5e86a31d3853032568b6','f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7']))) THEN
    RAISE EXCEPTION 'unexpected predecessor fence scenario_recorder_fence';
  END IF;

ALTER TABLE scenario_test_action_policies ADD COLUMN cold_start_spec jsonb
 CHECK(cold_start_spec IS NULL OR (jsonb_typeof(cold_start_spec)='object' AND cold_start_spec->>'version'='1'
  AND (cold_start_spec->>'within_ms')::bigint BETWEEN 1 AND 120000 AND simulation_plan IS NOT NULL AND network_mode='contained'));
CREATE FUNCTION scenario_cold_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.cold_start_spec IS DISTINCT FROM OLD.cold_start_spec THEN
 RAISE EXCEPTION 'cold-start plan is immutable' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
CREATE TRIGGER scenario_cold_policy_immutable BEFORE UPDATE ON scenario_test_action_policies FOR EACH ROW EXECUTE FUNCTION scenario_cold_policy_immutable();
CREATE TABLE scenario_cold_preparations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL UNIQUE REFERENCES apps(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK(run_id ~ '^[0-9a-f]{32}$'),
 deployment_id uuid NOT NULL,
 instance_id uuid,
 recorder_session_id uuid,
 state text NOT NULL CHECK(state IN ('armed','claimed','fenced','retired','ready','booted','failed')),
 boot_instance_id uuid,
 boot_recorder_session_id uuid,
 wake_id text,
 method text CHECK(method IS NULL OR method='cold_boot'),
 startup_ms bigint NOT NULL DEFAULT 0 CHECK(startup_ms>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 ready_at timestamptz,
 CHECK((instance_id IS NULL)=(recorder_session_id IS NULL)),
 CHECK((boot_instance_id IS NULL)=(boot_recorder_session_id IS NULL)),
 CHECK(boot_instance_id IS NULL OR (boot_instance_id IS DISTINCT FROM instance_id AND method='cold_boot' AND wake_id IS NOT NULL))
);
CREATE FUNCTION scenario_cold_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) AND EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id)
   THEN RAISE EXCEPTION 'cold-start evidence is retained until app deletion' USING ERRCODE='23514'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF p.cold_start_spec IS NULL OR p.sealed_at IS NOT NULL OR p.recorder_closing OR p.account_id<>NEW.account_id OR p.run_id<>NEW.run_id THEN
  RAISE EXCEPTION 'cold-start enrollment is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state NOT IN ('armed','retired') OR (NEW.state='armed')<>(NEW.instance_id IS NOT NULL)
   OR NEW.boot_instance_id IS NOT NULL OR NEW.ready_at IS NOT NULL THEN
   RAISE EXCEPTION 'cold-start preparation must begin unacknowledged' USING ERRCODE='23514'; END IF;
 ELSE
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.run_id,NEW.deployment_id,NEW.instance_id,NEW.recorder_session_id,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.run_id,OLD.deployment_id,OLD.instance_id,OLD.recorder_session_id,OLD.created_at)
   OR NOT((OLD.state='armed' AND NEW.state='claimed') OR (OLD.state='claimed' AND NEW.state='fenced')
    OR (OLD.state='fenced' AND NEW.state='retired') OR (OLD.state='retired' AND NEW.state='ready')
    OR (OLD.state='ready' AND NEW.state='booted') OR (OLD.state NOT IN ('booted','failed') AND NEW.state='failed'))
   OR (OLD.state<>'ready' AND (NEW.boot_instance_id,NEW.boot_recorder_session_id,NEW.wake_id,NEW.method,NEW.startup_ms)
     IS DISTINCT FROM (OLD.boot_instance_id,OLD.boot_recorder_session_id,OLD.wake_id,OLD.method,OLD.startup_ms))
   OR (NEW.state<>'ready' AND NEW.ready_at IS DISTINCT FROM OLD.ready_at) THEN
   RAISE EXCEPTION 'cold-start identity or lifecycle is immutable' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.state='ready' AND (EXISTS(SELECT 1 FROM instances WHERE app_id=NEW.app_id AND state NOT IN ('stopped','parked','failed'))
   OR EXISTS(SELECT 1 FROM snapshots s JOIN deployments d ON d.id=s.deployment_id WHERE d.app_id=NEW.app_id AND NOT s.stale)
   OR NEW.ready_at IS NULL) THEN
  RAISE EXCEPTION 'cold-start preparation requires drained instances and invalidated snapshots' USING ERRCODE='23514'; END IF;
 IF NEW.state='booted' AND NOT EXISTS(SELECT 1 FROM scenario_recorder_sessions r JOIN instances i ON i.id=r.instance_id
  WHERE r.id=NEW.boot_recorder_session_id AND r.app_id=NEW.app_id AND r.deployment_id=NEW.deployment_id AND r.status='recording'
  AND i.id=NEW.boot_instance_id AND i.app_id=NEW.app_id AND i.deployment_id=NEW.deployment_id AND i.wake_id::text=NEW.wake_id
  AND r.created_at>=NEW.ready_at AND i.state IN ('waking','cold_booting','running')) THEN
  RAISE EXCEPTION 'cold-start boot requires its original live recorder and new instance' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_cold_guard BEFORE INSERT OR UPDATE OR DELETE ON scenario_cold_preparations FOR EACH ROW EXECUTE FUNCTION scenario_cold_guard();

CREATE OR REPLACE FUNCTION scenario_rehearsal_execution_active(target_app uuid, execution_id uuid, deployment uuid DEFAULT NULL) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=target_app AND f.instance_id=execution_id AND f.state IN ('fenced','terminated','failed')) AND
 (EXISTS(SELECT 1 FROM instances i WHERE i.id=execution_id AND i.app_id=target_app
  AND (deployment IS NULL OR i.deployment_id=deployment) AND i.state IN ('waking','cold_booting','running','draining')
  AND NOT EXISTS(SELECT 1 FROM scenario_cold_preparations c WHERE c.app_id=target_app AND
   (c.state='failed' OR c.deployment_id<>i.deployment_id OR
    (c.state IN ('armed','claimed') AND c.instance_id IS DISTINCT FROM i.id) OR
    c.state IN ('fenced','retired') OR c.instance_id=i.id AND c.state IN ('ready','booted') OR
    c.state='booted' AND c.boot_instance_id<>i.id)))
 OR EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
  WHERE t.id=execution_id AND t.app_id=target_app AND t.account_id=p.account_id
  AND (deployment IS NULL OR t.deployment_id=deployment) AND t.status IN ('restoring','running')
  AND t.kind='manual' AND NOT t.command_shell AND t.max_output_bytes=65536
  AND scenario_rehearsal_command_spec(t.command)->>'run_id'=p.run_id
  AND scenario_rehearsal_command_spec(t.command)->>'deployment_id'=t.deployment_id::text
  AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL));
$$;

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
      AND f.run_id=policy.run_id AND f.state='terminated') AND NOT EXISTS
     (SELECT 1 FROM scenario_cold_preparations c WHERE c.recorder_session_id=NEW.id AND c.app_id=NEW.app_id
      AND c.instance_id=NEW.instance_id AND c.deployment_id=NEW.deployment_id AND c.account_id=policy.account_id
      AND c.run_id=policy.run_id AND c.state IN ('retired','ready','booted'))) OR EXISTS(SELECT 1 FROM scenario_recorder_requests
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
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_cold_preparations) OR EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE cold_start_spec IS NOT NULL) THEN RAISE EXCEPTION 'cold-start enrollment must be removed before downgrade'; END IF; END $$;
-- +goose StatementEnd
-- +goose StatementBegin
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
-- +goose StatementEnd
DROP TABLE scenario_cold_preparations;
DROP FUNCTION scenario_cold_guard();
DROP TRIGGER scenario_cold_policy_immutable ON scenario_test_action_policies;
DROP FUNCTION scenario_cold_policy_immutable();
ALTER TABLE scenario_test_action_policies DROP COLUMN cold_start_spec;
