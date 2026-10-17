-- +goose Up
SET LOCAL lock_timeout = '5s';
-- A missing ledger row may leave this migration's complete schema behind.
-- Preserve later fences only when every owned object and a known function
-- definition are present; refuse partial or unrecognized schema.
-- +goose StatementBegin
DO $rehearsal_replay$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_sessions') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_requests') AND relkind IN ('r','p'))
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_sessions') AND tgname='scenario_recorder_session_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_requests') AND tgname='scenario_recorder_request_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_recorder_closing_fence' AND NOT tgisinternal)
    AND EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='recorder_closing' AND attnum>0 AND NOT attisdropped)
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['fea9d2738dcb6849294acd8fc0329c6a','f6368b62b0f1062b37392c3a790a1eec','63acbe9923cf5e86a31d3853032568b6','f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7']))
    AND EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_closing_fence()') AND md5(prosrc)=ANY(ARRAY['9784e5b435d184cbc706a50f2f37542a'])) THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_sessions') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_class WHERE oid=to_regclass('scenario_recorder_requests') AND relkind IN ('r','p'))
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_sessions') AND tgname='scenario_recorder_session_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_recorder_requests') AND tgname='scenario_recorder_request_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('scenario_test_action_policies') AND tgname='scenario_recorder_closing_fence' AND NOT tgisinternal)
    OR EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid=to_regclass('scenario_test_action_policies') AND attname='recorder_closing' AND attnum>0 AND NOT attisdropped)
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_fence()') AND md5(prosrc)=ANY(ARRAY['fea9d2738dcb6849294acd8fc0329c6a','f6368b62b0f1062b37392c3a790a1eec','63acbe9923cf5e86a31d3853032568b6','f3638ec3d4147fff208772c6a104d663','1723aaeb805fb945bed86404d881b0c7']))
    OR EXISTS (SELECT 1 FROM pg_proc WHERE oid=to_regprocedure('scenario_recorder_closing_fence()') AND md5(prosrc)=ANY(ARRAY['9784e5b435d184cbc706a50f2f37542a'])) THEN
    RAISE EXCEPTION 'partial or unrecognized schema for rehearsal migration 7; inspect recovery before continuing';
  END IF;

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
CREATE TRIGGER scenario_recorder_session_fence BEFORE INSERT OR UPDATE ON scenario_recorder_sessions FOR EACH ROW EXECUTE FUNCTION scenario_recorder_fence();
CREATE TRIGGER scenario_recorder_request_fence BEFORE INSERT OR UPDATE ON scenario_recorder_requests FOR EACH ROW EXECUTE FUNCTION scenario_recorder_fence();

CREATE FUNCTION scenario_recorder_closing_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.recorder_closing AND NOT NEW.recorder_closing THEN
  RAISE EXCEPTION 'recorder closing is irreversible' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER scenario_recorder_closing_fence BEFORE UPDATE ON scenario_test_action_policies FOR EACH ROW EXECUTE FUNCTION scenario_recorder_closing_fence();
END
$rehearsal_replay$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
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
-- Command helpers remain while their stronger recorder fence can reference them.
DROP FUNCTION IF EXISTS scenario_rehearsal_execution_active(uuid,uuid,uuid);
DROP FUNCTION IF EXISTS scenario_rehearsal_command_spec(text[]);
ALTER TABLE scenario_test_action_policies DROP COLUMN recorder_closing;
