-- +goose Up
ALTER TABLE scenario_test_action_policies ADD COLUMN recorder_closing boolean NOT NULL DEFAULT false;

CREATE TABLE scenario_recorder_sessions (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 instance_id uuid NOT NULL CHECK (instance_id<>'00000000-0000-0000-0000-000000000000'),
 deployment_id uuid NOT NULL CHECK (deployment_id<>'00000000-0000-0000-0000-000000000000'),
 status text NOT NULL DEFAULT 'recording' CHECK (status IN ('recording','closed','failed')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (app_id,instance_id)
);
CREATE TABLE scenario_recorder_requests (
 id uuid PRIMARY KEY,
 session_id uuid NOT NULL REFERENCES scenario_recorder_sessions(id) ON DELETE CASCADE,
 attempt_id uuid UNIQUE REFERENCES scenario_simulation_attempts(id) ON DELETE CASCADE,
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','admitted','completed','failed')),
 CHECK ((status='pending')=(attempt_id IS NULL))
);

-- +goose StatementBegin
CREATE FUNCTION scenario_recorder_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target_app uuid;
BEGIN
 IF TG_TABLE_NAME='scenario_recorder_sessions' THEN
  target_app:=NEW.app_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.app_id,NEW.instance_id,NEW.deployment_id,NEW.created_at)
       IS DISTINCT FROM (OLD.id,OLD.app_id,OLD.instance_id,OLD.deployment_id,OLD.created_at)
       OR OLD.status<>'recording' OR NEW.status NOT IN ('closed','failed')) THEN
   RAISE EXCEPTION 'recorder identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 ELSE
  SELECT app_id INTO target_app FROM scenario_recorder_sessions WHERE id=NEW.session_id;
  IF TG_OP='UPDATE' AND ((NEW.id,NEW.session_id) IS DISTINCT FROM (OLD.id,OLD.session_id)
       OR (OLD.attempt_id IS NOT NULL AND NEW.attempt_id IS DISTINCT FROM OLD.attempt_id)
       OR NOT ((OLD.status='pending' AND NEW.status IN ('admitted','failed'))
            OR (OLD.status='admitted' AND NEW.status IN ('completed','failed')))) THEN
   RAISE EXCEPTION 'recorder request identity or terminal receipt is immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 PERFORM id FROM apps WHERE id=target_app FOR KEY SHARE;
 IF NOT EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE app_id=target_app
      AND simulation_plan IS NOT NULL AND sealed_at IS NULL) THEN
  RAISE EXCEPTION 'recorder window is unavailable or sealed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_recorder_session_fence BEFORE INSERT OR UPDATE ON scenario_recorder_sessions FOR EACH ROW EXECUTE FUNCTION scenario_recorder_fence();
CREATE TRIGGER scenario_recorder_request_fence BEFORE INSERT OR UPDATE ON scenario_recorder_requests FOR EACH ROW EXECUTE FUNCTION scenario_recorder_fence();

-- +goose StatementBegin
CREATE FUNCTION scenario_recorder_closing_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.recorder_closing AND NOT NEW.recorder_closing THEN
  RAISE EXCEPTION 'recorder closing is irreversible' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER scenario_recorder_closing_fence BEFORE UPDATE ON scenario_test_action_policies FOR EACH ROW EXECUTE FUNCTION scenario_recorder_closing_fence();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM scenario_test_action_policies WHERE simulation_plan IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove recorder fencing while simulation enrollment remains';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER scenario_recorder_closing_fence ON scenario_test_action_policies;
DROP FUNCTION scenario_recorder_closing_fence();
DROP TABLE scenario_recorder_requests;
DROP TABLE scenario_recorder_sessions;
DROP FUNCTION scenario_recorder_fence();
ALTER TABLE scenario_test_action_policies DROP COLUMN recorder_closing;
