-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION scenario_rehearsal_command_spec(argv text[]) RETURNS jsonb LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE spec jsonb;
BEGIN
 IF argv[1] IS DISTINCT FROM '__gregale_rehearsal_command_v1__' OR cardinality(argv) NOT BETWEEN 2 AND 5 THEN RETURN NULL; END IF;
 BEGIN spec:=array_to_string(argv[2:cardinality(argv)],'')::jsonb;
 EXCEPTION WHEN invalid_text_representation THEN RETURN NULL; END;
 IF octet_length(array_to_string(argv,''))>16384 OR spec->>'version' IS DISTINCT FROM '1'
  OR coalesce(spec->>'run_id','')!~'^[0-9a-f]{32}$'
  OR coalesce(spec->>'deployment_id','')!~'^[0-9a-f-]{36}$' THEN RETURN NULL; END IF;
 RETURN spec;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION scenario_rehearsal_execution_active(target_app uuid, execution_id uuid, deployment uuid DEFAULT NULL) RETURNS boolean LANGUAGE sql STABLE AS $$
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
CREATE FUNCTION scenario_rehearsal_command_admission() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER scenario_rehearsal_command_admission BEFORE INSERT ON app_tasks FOR EACH ROW EXECUTE FUNCTION scenario_rehearsal_command_admission();

ALTER TABLE scenario_recorder_sessions ADD COLUMN command_result text
 CHECK (command_result IS NULL OR (status='closed' AND octet_length(command_result) BETWEEN 1 AND 65536));

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


-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove command recorder fencing while simulation enrollment remains';
 END IF;
END $$;
-- +goose StatementEnd
-- Keep helper functions and the stronger recorder fence until its owning
-- recorder migration is removed. Removing either early would relax admission.
DROP TRIGGER scenario_rehearsal_command_admission ON app_tasks;
DROP FUNCTION scenario_rehearsal_command_admission();
