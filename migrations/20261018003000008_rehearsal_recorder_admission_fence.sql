-- +goose Up
SET LOCAL lock_timeout = '5s';
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
  IF TG_OP='INSERT' AND NOT EXISTS(SELECT 1 FROM instances i JOIN apps a ON a.id=i.app_id
      WHERE i.id=NEW.instance_id AND i.app_id=NEW.app_id AND i.deployment_id=NEW.deployment_id
       AND a.account_id=policy.account_id AND i.state IN ('waking','cold_booting','running','draining')) THEN
   RAISE EXCEPTION 'recorder execution identity is unavailable' USING ERRCODE='23514';
  END IF;
  IF NEW.status='closed' AND (NOT policy.recorder_closing OR EXISTS(SELECT 1 FROM scenario_recorder_requests
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
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot relax recorder admission while simulation enrollment remains';
 END IF;
END $$;
-- +goose StatementEnd
-- Retain the stronger fence on downgrade; the preceding migration owns removal.
