-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE scenario_test_action_policies ADD COLUMN suspend_resume_spec jsonb
 CHECK(suspend_resume_spec IS NULL OR (jsonb_typeof(suspend_resume_spec)='object'
  AND suspend_resume_spec->>'version'='1' AND suspend_resume_spec->>'checkpoint'='after_setup'
  AND jsonb_typeof(suspend_resume_spec->'workloads')='array'
  AND jsonb_array_length(suspend_resume_spec->'workloads') BETWEEN 1 AND 16
  AND (suspend_resume_spec->>'pause_ms')::bigint BETWEEN 1 AND 30000
  AND (suspend_resume_spec->>'within_ms')::bigint BETWEEN 1 AND 120000
  AND simulation_plan IS NOT NULL AND network_mode='contained' AND cold_start_spec IS NULL
  AND managed_work_plan->'worker_termination' IS NULL));
-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_policy_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.suspend_resume_spec IS DISTINCT FROM OLD.suspend_resume_spec THEN
 RAISE EXCEPTION 'suspend/resume plan is immutable' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_suspend_policy_immutable BEFORE UPDATE ON scenario_test_action_policies FOR EACH ROW EXECUTE FUNCTION scenario_suspend_policy_immutable();
CREATE TABLE scenario_suspend_resumes (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL UNIQUE REFERENCES apps(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK(run_id ~ '^[0-9a-f]{32}$'),
 deployment_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 recorder_session_id uuid NOT NULL,
 state text NOT NULL CHECK(state IN ('armed','claimed','fenced','paused','resuming','resumed','complete','failed')),
 method text CHECK(method IS NULL OR method='in_place'),
 pause_ms bigint NOT NULL DEFAULT 0 CHECK(pause_ms>=0),
 resume_ms bigint NOT NULL DEFAULT 0 CHECK(resume_ms>=0),
 created_at timestamptz NOT NULL DEFAULT now(),
 paused_at timestamptz,
 CHECK(state NOT IN ('resumed','complete') OR method='in_place')
);
-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) AND EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id)
   THEN RAISE EXCEPTION 'suspend/resume evidence is retained until app deletion' USING ERRCODE='23514'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF p.suspend_resume_spec IS NULL OR NOT (p.suspend_resume_spec->'workloads' ? p.workload_name)
  OR p.sealed_at IS NOT NULL OR p.recorder_closing OR p.simulation_failed
  OR p.account_id<>NEW.account_id OR p.run_id<>NEW.run_id THEN
  RAISE EXCEPTION 'suspend/resume enrollment unavailable or sealed' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'armed' OR NEW.method IS NOT NULL OR NEW.pause_ms<>0 OR NEW.resume_ms<>0 OR NEW.paused_at IS NOT NULL THEN
   RAISE EXCEPTION 'suspend/resume must begin unacknowledged' USING ERRCODE='23514'; END IF;
 ELSE
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.run_id,NEW.deployment_id,NEW.instance_id,NEW.recorder_session_id,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.run_id,OLD.deployment_id,OLD.instance_id,OLD.recorder_session_id,OLD.created_at)
   OR NOT ((OLD.state='armed' AND NEW.state='claimed') OR (OLD.state='claimed' AND NEW.state='fenced')
    OR (OLD.state='fenced' AND NEW.state='paused') OR (OLD.state='paused' AND NEW.state='resuming')
    OR (OLD.state='resuming' AND NEW.state='resumed') OR (OLD.state='resumed' AND NEW.state='complete')
    OR (OLD.state NOT IN ('complete','failed') AND NEW.state='failed'))
   OR (NEW.state<>'paused' AND NEW.paused_at IS DISTINCT FROM OLD.paused_at)
   OR (NEW.state<>'resuming' AND NEW.pause_ms<>OLD.pause_ms)
   OR (NEW.state<>'resumed' AND (NEW.method,NEW.resume_ms) IS DISTINCT FROM (OLD.method,OLD.resume_ms)) THEN
   RAISE EXCEPTION 'suspend/resume identity or lifecycle is immutable' USING ERRCODE='23514'; END IF;
 END IF;
 IF NEW.state<>'failed' AND NOT EXISTS(SELECT 1 FROM scenario_recorder_sessions r JOIN instances i ON i.id=r.instance_id
  JOIN apps a ON a.id=i.app_id JOIN scenario_test_members m ON m.app_id=a.id
  WHERE r.id=NEW.recorder_session_id AND r.app_id=NEW.app_id AND r.deployment_id=NEW.deployment_id AND r.status='recording'
   AND i.id=NEW.instance_id AND i.app_id=NEW.app_id AND i.deployment_id=NEW.deployment_id AND i.tail_count=0
   AND i.state=CASE WHEN NEW.state IN ('armed','claimed','complete') THEN 'running' ELSE 'frozen' END
   AND a.account_id=NEW.account_id AND a.status='active' AND a.preview_of_slug IS NOT NULL
   AND a.preview_pr_number=0 AND a.preview_pr_state='open' AND a.preview_expires_at>clock_timestamp()
   AND m.account_id=NEW.account_id AND m.run_id=NEW.run_id) THEN
  RAISE EXCEPTION 'suspend/resume requires its original live VM and recorder' USING ERRCODE='23514'; END IF;
 IF NEW.state IN ('armed','claimed','fenced') AND (
  EXISTS(SELECT 1 FROM scenario_recorder_requests q WHERE q.session_id=NEW.recorder_session_id AND q.status<>'completed')
  OR EXISTS(SELECT 1 FROM invocations v JOIN scenario_test_action_policies w ON w.app_id=v.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id)
  OR EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies w ON w.app_id=t.app_id
    WHERE w.account_id=p.account_id AND w.run_id=p.run_id AND t.status IN ('queued','restoring','running'))
  OR EXISTS(SELECT 1 FROM instances i WHERE i.app_id=NEW.app_id AND i.id<>NEW.instance_id AND i.state NOT IN ('stopped','parked','failed'))
  OR EXISTS(SELECT 1 FROM exclusive_work_operations o WHERE o.app_id=NEW.app_id AND o.state IN ('pending','running'))
  OR EXISTS(SELECT 1 FROM workflow_runs w WHERE w.app_id=NEW.app_id AND w.status IN ('pending','running','awaiting_event'))
  OR (SELECT count(*) FROM deployments WHERE app_id=NEW.app_id)<>1) THEN
  RAISE EXCEPTION 'suspend/resume checkpoint has unsettled work' USING ERRCODE='23514'; END IF;
 IF NEW.state='paused' AND NEW.paused_at IS NULL OR NEW.state IN ('resuming','resumed','complete')
  AND (NEW.paused_at IS NULL OR NEW.pause_ms<(p.suspend_resume_spec->>'pause_ms')::bigint) THEN
  RAISE EXCEPTION 'suspend/resume pause proof is missing' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_suspend_guard BEFORE INSERT OR UPDATE OR DELETE ON scenario_suspend_resumes FOR EACH ROW EXECUTE FUNCTION scenario_suspend_guard();

-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_execution_active(target_app uuid, execution_id uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS(SELECT 1 FROM scenario_suspend_resumes r WHERE r.app_id=target_app AND
  (r.state IN ('fenced','paused','failed') OR (r.instance_id<>execution_id AND EXISTS(SELECT 1 FROM instances i WHERE i.id=execution_id))));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE;
BEGIN
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF p.suspend_resume_spec IS NOT NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
  IF EXISTS(SELECT 1 FROM scenario_suspend_resumes r WHERE r.account_id=p.account_id AND r.run_id=p.run_id AND r.state<>'complete') THEN
   RAISE EXCEPTION 'work must wait for the suspend/resume checkpoint' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_suspend_work_guard BEFORE INSERT ON invocations FOR EACH ROW EXECUTE FUNCTION scenario_suspend_work_guard();
CREATE TRIGGER scenario_suspend_command_guard BEFORE INSERT ON app_tasks FOR EACH ROW EXECUTE FUNCTION scenario_suspend_work_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_rehearsal_execution_active(target_app uuid, execution_id uuid, deployment uuid DEFAULT NULL) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT scenario_suspend_execution_active(target_app,execution_id) AND NOT EXISTS(SELECT 1 FROM scenario_worker_faults f WHERE f.app_id=target_app AND f.instance_id=execution_id AND f.state IN ('fenced','terminated','failed')) AND
 (EXISTS(SELECT 1 FROM instances i WHERE i.id=execution_id AND i.app_id=target_app
  AND (deployment IS NULL OR i.deployment_id=deployment) AND (i.state IN ('waking','cold_booting','running','draining') OR i.state='frozen' AND EXISTS
   (SELECT 1 FROM scenario_suspend_resumes r WHERE r.app_id=target_app AND r.instance_id=i.id AND r.state IN ('resuming','resumed')))
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
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE suspend_resume_spec IS NOT NULL)
 THEN RAISE EXCEPTION 'suspend/resume evidence must be retained until its apps are deleted'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_suspend_command_guard ON app_tasks;
DROP TRIGGER scenario_suspend_work_guard ON invocations;
DROP FUNCTION scenario_suspend_work_guard();
-- +goose StatementBegin
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
-- +goose StatementEnd
DROP FUNCTION scenario_suspend_execution_active(uuid,uuid);
DROP TABLE scenario_suspend_resumes;
DROP FUNCTION scenario_suspend_guard();
DROP TRIGGER scenario_suspend_policy_immutable ON scenario_test_action_policies;
DROP FUNCTION scenario_suspend_policy_immutable();
ALTER TABLE scenario_test_action_policies DROP COLUMN suspend_resume_spec;
