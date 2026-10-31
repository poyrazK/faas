-- filename: 20261020150000001_rehearsal_contained_release.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A contained rehearsal runs its candidate's release command (for example a
-- database migration) as the run's pinned contained command instead of
-- refusing it (ADR-20261020150000). The release keeps kind 'release' so the
-- deployment's prime gate still waits for it; it must equal the snapshotting
-- deployment's own release command and carry no extra env or files.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_rehearsal_release_matches(command text[], deployment uuid, release_command text[], release_shell boolean) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(cardinality(release_command)>0
  AND (NOT release_shell OR cardinality(release_command)=1)
  AND scenario_rehearsal_command_spec(command)->>'deployment_id'=deployment::text
  AND NOT (scenario_rehearsal_command_spec(command) ? 'env')
  AND NOT (scenario_rehearsal_command_spec(command) ? 'files')
  AND scenario_rehearsal_command_spec(command)->'command'=to_jsonb(CASE WHEN release_shell
   THEN ARRAY['/bin/sh','-lc',release_command[1]] ELSE release_command END),false);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_rehearsal_command_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE policy scenario_test_action_policies%ROWTYPE; spec jsonb;
BEGIN
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 spec:=scenario_rehearsal_command_spec(NEW.command);
 IF policy.network_mode IS DISTINCT FROM 'contained' AND NEW.command[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__' THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:' || policy.account_id::text || ':' || policy.run_id,0));
 PERFORM id FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF spec IS NULL OR policy.app_id IS NULL OR policy.account_id<>NEW.account_id
  OR policy.network_mode IS DISTINCT FROM 'contained' OR policy.simulation_plan IS NULL
  OR policy.sealed_at IS NOT NULL OR policy.recorder_closing OR policy.simulation_failed
  OR spec->>'run_id'<>policy.run_id OR spec->>'deployment_id'<>NEW.deployment_id::text
  OR NEW.command_shell OR NEW.max_output_bytes<>65536
  OR NOT (NEW.kind='manual' AND EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id AND status='live')
   OR NEW.kind='release' AND EXISTS(SELECT 1 FROM deployments d WHERE d.id=NEW.deployment_id AND d.app_id=NEW.app_id AND d.status='snapshotting'
    AND scenario_rehearsal_release_matches(NEW.command,d.id,d.release_command,d.release_command_shell)))
  OR EXISTS(SELECT 1 FROM app_task_attach_sessions WHERE task_id=NEW.id) THEN
  RAISE EXCEPTION 'contained command admission is unavailable' USING ERRCODE='23514';
 END IF;
 IF (SELECT count(*) FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
     WHERE p.account_id=policy.account_id AND p.run_id=policy.run_id)>=256 THEN
  RAISE EXCEPTION 'contained command evidence bound exceeded' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

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
  AND t.kind IN ('manual','release') AND NOT t.command_shell AND t.max_output_bytes=65536
  AND scenario_rehearsal_command_spec(t.command)->>'run_id'=p.run_id
  AND scenario_rehearsal_command_spec(t.command)->>'deployment_id'=t.deployment_id::text
  AND p.network_mode='contained' AND p.simulation_plan IS NOT NULL AND p.sealed_at IS NULL));
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM app_tasks WHERE kind='release' AND command[1]='__gregale_rehearsal_command_v1__')
 THEN RAISE EXCEPTION 'contained rehearsal release tasks are retained run evidence'; END IF; END $$;
-- +goose StatementEnd
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION scenario_rehearsal_command_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE policy scenario_test_action_policies%ROWTYPE; spec jsonb;
BEGIN
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 spec:=scenario_rehearsal_command_spec(NEW.command);
 IF policy.network_mode IS DISTINCT FROM 'contained' AND NEW.command[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__' THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('scenario-action-run:' || policy.account_id::text || ':' || policy.run_id,0));
 PERFORM id FROM apps WHERE id=NEW.app_id FOR KEY SHARE;
 SELECT * INTO policy FROM scenario_test_action_policies WHERE app_id=NEW.app_id;
 IF spec IS NULL OR policy.app_id IS NULL OR policy.account_id<>NEW.account_id
  OR policy.network_mode IS DISTINCT FROM 'contained' OR policy.simulation_plan IS NULL
  OR policy.sealed_at IS NOT NULL OR policy.recorder_closing OR policy.simulation_failed
  OR spec->>'run_id'<>policy.run_id OR spec->>'deployment_id'<>NEW.deployment_id::text
  OR NEW.kind<>'manual' OR NEW.command_shell OR NEW.max_output_bytes<>65536
  OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=NEW.deployment_id AND app_id=NEW.app_id AND status='live')
  OR EXISTS(SELECT 1 FROM app_task_attach_sessions WHERE task_id=NEW.id) THEN
  RAISE EXCEPTION 'contained command admission is unavailable' USING ERRCODE='23514';
 END IF;
 IF (SELECT count(*) FROM app_tasks t JOIN scenario_test_action_policies p ON p.app_id=t.app_id
     WHERE p.account_id=policy.account_id AND p.run_id=policy.run_id)>=256 THEN
  RAISE EXCEPTION 'contained command evidence bound exceeded' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP FUNCTION scenario_rehearsal_release_matches(text[],uuid,text[],boolean);
