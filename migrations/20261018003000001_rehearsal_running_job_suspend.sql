-- +goose Up
-- ADR-1359: retained first-dispatch and broker checkpoint identity.
ALTER TABLE scenario_test_action_policies DROP CONSTRAINT scenario_test_action_policies_check3;
ALTER TABLE scenario_test_action_policies ADD CONSTRAINT scenario_suspend_plan_check CHECK(suspend_resume_spec IS NULL OR ((jsonb_typeof(suspend_resume_spec)='object'
 AND suspend_resume_spec->>'version'='1' AND jsonb_typeof(suspend_resume_spec->'workloads')='array'
 AND jsonb_array_length(suspend_resume_spec->'workloads') BETWEEN 1 AND 16
 AND (suspend_resume_spec->>'pause_ms')::bigint BETWEEN 1 AND 30000
 AND (suspend_resume_spec->>'within_ms')::bigint BETWEEN 1 AND 120000
 AND simulation_plan IS NOT NULL AND network_mode='contained' AND cold_start_spec IS NULL
 AND managed_work_plan->'worker_termination' IS NULL
 AND ((suspend_resume_spec->>'checkpoint'='after_setup' AND NOT suspend_resume_spec ? 'worker_checkpoint') OR
  (suspend_resume_spec->>'checkpoint'='managed_job' AND jsonb_array_length(suspend_resume_spec->'workloads')=1
   AND suspend_resume_spec->>'worker_checkpoint' ~ '^[a-z][a-z0-9-]{0,62}$' AND managed_work_plan IS NOT NULL))) IS TRUE));
ALTER TABLE scenario_suspend_resumes
 ADD COLUMN invocation_id uuid,
 ADD COLUMN invocation_attempt integer,
 ADD COLUMN checkpoint_attempt_id uuid,
 ADD CONSTRAINT scenario_suspend_dispatch_identity CHECK(
 (invocation_id IS NULL AND invocation_attempt IS NULL AND checkpoint_attempt_id IS NULL) OR
 (invocation_id IS NOT NULL AND invocation_attempt=1 AND checkpoint_attempt_id IS NOT NULL) IS TRUE);
ALTER TABLE scenario_suspend_resumes DROP CONSTRAINT scenario_suspend_resumes_state_check;
ALTER TABLE scenario_suspend_resumes ADD CONSTRAINT scenario_suspend_resumes_state_check
 CHECK(state IN ('waiting','armed','claimed','fenced','paused','resuming','resumed','complete','failed'));
-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_job_active(r scenario_suspend_resumes) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM invocations v WHERE v.id=r.invocation_id AND v.app_id=r.app_id
  AND nullif(v.instance_id,'')::uuid=r.instance_id AND v.state='dispatching' AND v.source='async_invoke'
  AND v.attempts=r.invocation_attempt AND v.attempts=1 AND v.lease_expires_at>clock_timestamp()
  AND v.replay_generation=0)
 AND (SELECT count(*) FROM invocations v JOIN scenario_test_action_policies p ON p.app_id=v.app_id
  WHERE p.account_id=r.account_id AND p.run_id=r.run_id)=1;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_suspend_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
  IF NEW.state<>(CASE WHEN p.suspend_resume_spec->>'checkpoint'='managed_job' THEN 'waiting' ELSE 'armed' END) OR NEW.method IS NOT NULL OR NEW.pause_ms<>0 OR NEW.resume_ms<>0 OR NEW.paused_at IS NOT NULL THEN
   RAISE EXCEPTION 'suspend/resume must begin unacknowledged' USING ERRCODE='23514'; END IF;
 ELSE
  IF (NEW.id,NEW.account_id,NEW.app_id,NEW.run_id,NEW.deployment_id,NEW.instance_id,NEW.recorder_session_id,NEW.invocation_id,NEW.invocation_attempt,NEW.checkpoint_attempt_id,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.app_id,OLD.run_id,OLD.deployment_id,OLD.instance_id,OLD.recorder_session_id,OLD.invocation_id,OLD.invocation_attempt,OLD.checkpoint_attempt_id,OLD.created_at)
   OR NOT ((OLD.state='waiting' AND NEW.state='armed') OR (OLD.state='armed' AND NEW.state='claimed') OR (OLD.state='claimed' AND NEW.state='fenced')
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
   AND i.state=CASE WHEN NEW.state IN ('waiting','armed','claimed','complete') THEN 'running' ELSE 'frozen' END
   AND a.account_id=NEW.account_id AND a.status='active' AND a.preview_of_slug IS NOT NULL
   AND a.preview_pr_number=0 AND a.preview_pr_state='open' AND a.preview_expires_at>clock_timestamp()
   AND m.account_id=NEW.account_id AND m.run_id=NEW.run_id) THEN
  RAISE EXCEPTION 'suspend/resume requires its original live VM and recorder' USING ERRCODE='23514'; END IF;
 IF NEW.state<>'failed' AND (
  EXISTS(SELECT 1 FROM scenario_recorder_requests q WHERE q.session_id=NEW.recorder_session_id AND q.status<>'completed' AND NOT (NEW.state='waiting' AND q.status='admitted' AND q.attempt_id=NEW.checkpoint_attempt_id))
  OR EXISTS(SELECT 1 FROM invocations v JOIN scenario_test_action_policies w ON w.app_id=v.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id AND (NEW.invocation_id IS NULL OR v.id<>NEW.invocation_id))
  OR EXISTS(SELECT 1 FROM app_tasks t JOIN scenario_test_action_policies w ON w.app_id=t.app_id
    WHERE w.account_id=p.account_id AND w.run_id=p.run_id AND t.status IN ('queued','restoring','running') AND NEW.state<>'waiting')
  OR EXISTS(SELECT 1 FROM instances i WHERE i.app_id=NEW.app_id AND i.id<>NEW.instance_id AND i.state NOT IN ('stopped','parked','failed'))
  OR EXISTS(SELECT 1 FROM exclusive_work_operations o WHERE o.app_id=NEW.app_id AND o.state IN ('pending','running'))
  OR EXISTS(SELECT 1 FROM workflow_runs w WHERE w.app_id=NEW.app_id AND w.status IN ('pending','running','awaiting_event'))
  OR (SELECT count(*) FROM deployments WHERE app_id=NEW.app_id)<>1) THEN
  RAISE EXCEPTION 'suspend/resume checkpoint has unsettled work' USING ERRCODE='23514'; END IF;
 IF (p.suspend_resume_spec->>'checkpoint'='managed_job') IS DISTINCT FROM (NEW.invocation_id IS NOT NULL)
  OR NEW.state<>'failed' AND NEW.invocation_id IS NOT NULL AND NOT scenario_suspend_job_active(NEW) THEN
  RAISE EXCEPTION 'suspend/resume enrolled dispatch changed or is unavailable' USING ERRCODE='23514'; END IF;
 IF NEW.state='armed' AND NEW.invocation_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM scenario_simulation_attempts a
   WHERE a.id=NEW.checkpoint_attempt_id AND a.app_id=NEW.app_id AND a.evidence->>'status'='completed'
   AND a.evidence->>'fixture'=p.suspend_resume_spec->>'worker_checkpoint') THEN
  RAISE EXCEPTION 'suspend/resume checkpoint has not completed at its broker' USING ERRCODE='23514'; END IF;
 IF NEW.state='paused' AND NEW.paused_at IS NULL OR NEW.state IN ('resuming','resumed','complete')
  AND (NEW.paused_at IS NULL OR NEW.pause_ms<(p.suspend_resume_spec->>'pause_ms')::bigint) THEN
  RAISE EXCEPTION 'suspend/resume pause proof is missing' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_suspend_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p scenario_test_action_policies%ROWTYPE;
BEGIN
 SELECT * INTO p FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF p.suspend_resume_spec IS NOT NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:'||p.account_id::text||':'||p.run_id,0));
  IF TG_TABLE_NAME='invocations' AND p.suspend_resume_spec->>'checkpoint'='managed_job' THEN
   IF (p.workload_name<>(p.suspend_resume_spec->'workloads'->>0) OR NEW.source<>'async_invoke' OR
     EXISTS(SELECT 1 FROM invocations v JOIN scenario_test_action_policies w ON w.app_id=v.app_id WHERE w.account_id=p.account_id AND w.run_id=p.run_id)) THEN
    RAISE EXCEPTION 'running-job suspend supports one managed job in its run' USING ERRCODE='23514'; END IF;
  END IF;
  IF EXISTS(SELECT 1 FROM scenario_suspend_resumes r WHERE r.account_id=p.account_id AND r.run_id=p.run_id AND r.state<>'complete') THEN
   RAISE EXCEPTION 'work must wait for the suspend/resume checkpoint' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- A lease loss remains journaled, but an uncertain pause cannot dispatch a new
-- attempt while the original response/VM may still be held. Renewal is allowed.
-- +goose StatementBegin
CREATE FUNCTION scenario_suspend_dispatch_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state='dispatching' AND (NEW.state,NEW.attempts,NEW.instance_id) IS DISTINCT FROM (OLD.state,OLD.attempts,OLD.instance_id)
  AND EXISTS(SELECT 1 FROM scenario_suspend_resumes r WHERE r.app_id=NEW.app_id AND r.invocation_id IS NOT NULL AND r.state<>'complete') THEN
  RAISE EXCEPTION 'managed dispatch must wait for the original suspend/resume checkpoint' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_suspend_dispatch_guard BEFORE UPDATE OF state,attempts,instance_id ON invocations FOR EACH ROW EXECUTE FUNCTION scenario_suspend_dispatch_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE suspend_resume_spec->>'checkpoint'='managed_job')
 THEN RAISE EXCEPTION 'running-job checkpoint evidence is retained until app deletion'; END IF; END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_suspend_dispatch_guard ON invocations;
DROP FUNCTION scenario_suspend_dispatch_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_suspend_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_suspend_work_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
DROP FUNCTION scenario_suspend_job_active(scenario_suspend_resumes);
ALTER TABLE scenario_suspend_resumes DROP CONSTRAINT scenario_suspend_dispatch_identity,
 DROP COLUMN invocation_id,DROP COLUMN invocation_attempt,DROP COLUMN checkpoint_attempt_id;
ALTER TABLE scenario_suspend_resumes DROP CONSTRAINT scenario_suspend_resumes_state_check;
ALTER TABLE scenario_suspend_resumes ADD CONSTRAINT scenario_suspend_resumes_state_check
 CHECK(state IN ('armed','claimed','fenced','paused','resuming','resumed','complete','failed'));
ALTER TABLE scenario_test_action_policies DROP CONSTRAINT scenario_suspend_plan_check;
ALTER TABLE scenario_test_action_policies ADD CONSTRAINT scenario_test_action_policies_check3 CHECK(suspend_resume_spec IS NULL OR (jsonb_typeof(suspend_resume_spec)='object'
  AND suspend_resume_spec->>'version'='1' AND suspend_resume_spec->>'checkpoint'='after_setup'
  AND jsonb_typeof(suspend_resume_spec->'workloads')='array'
  AND jsonb_array_length(suspend_resume_spec->'workloads') BETWEEN 1 AND 16
  AND (suspend_resume_spec->>'pause_ms')::bigint BETWEEN 1 AND 30000
  AND (suspend_resume_spec->>'within_ms')::bigint BETWEEN 1 AND 120000
  AND simulation_plan IS NOT NULL AND network_mode='contained' AND cold_start_spec IS NULL
  AND managed_work_plan->'worker_termination' IS NULL));
