-- +goose Up
-- +goose StatementBegin
-- Install only after earlier task writers have committed. Restoring may
-- already own a native VM before the command's running dispatch fence.
LOCK TABLE app_tasks IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE regional_app_task_billing_history_start (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO regional_app_task_billing_history_start(singleton) VALUES (true);

CREATE TABLE regional_app_task_billing_intervals (
 id bigserial PRIMARY KEY,
 app_id uuid NOT NULL CHECK (app_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 task_id uuid NOT NULL CHECK (task_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 deployment_id uuid NOT NULL CHECK (deployment_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 run_id uuid NOT NULL CHECK (run_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 lease_owner text NOT NULL,
 started_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 complete_start boolean NOT NULL,
 ended_at timestamptz,
 unknown_since timestamptz,
 CHECK (ended_at IS NULL OR ended_at >= started_at),
 CHECK (unknown_since IS NULL OR unknown_since >= started_at),
 CHECK (ended_at IS NULL OR unknown_since IS NULL)
);
CREATE UNIQUE INDEX regional_app_task_billing_open_idx
 ON regional_app_task_billing_intervals(task_id) WHERE ended_at IS NULL AND unknown_since IS NULL;
CREATE INDEX regional_app_task_billing_app_page_idx ON regional_app_task_billing_intervals(app_id,id);

INSERT INTO regional_app_task_billing_intervals
 (app_id,task_id,deployment_id,run_id,lease_owner,started_at,recorded_at,complete_start)
SELECT t.app_id,t.id,t.deployment_id,t.lease_token,t.lease_owner,s.started_at,s.started_at,false
FROM app_tasks t CROSS JOIN regional_app_task_billing_history_start s
WHERE t.status IN ('restoring','running');

CREATE FUNCTION refuse_regional_app_task_billing_history_truncate()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 RAISE EXCEPTION 'regional app task billing history is retained' USING ERRCODE='23514';
END
$function$;

CREATE FUNCTION guard_regional_app_task_billing_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 IF TG_OP='DELETE' OR pg_trigger_depth() < 2 THEN
  RAISE EXCEPTION 'regional app task billing history is lifecycle owned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (
  (NEW.id,NEW.app_id,NEW.task_id,NEW.deployment_id,NEW.run_id,NEW.lease_owner,
   NEW.started_at,NEW.recorded_at,NEW.complete_start) IS DISTINCT FROM
  (OLD.id,OLD.app_id,OLD.task_id,OLD.deployment_id,OLD.run_id,OLD.lease_owner,
   OLD.started_at,OLD.recorded_at,OLD.complete_start)
  OR OLD.ended_at IS NOT NULL OR OLD.unknown_since IS NOT NULL
  OR (NEW.ended_at IS NULL AND NEW.unknown_since IS NULL)) THEN
  RAISE EXCEPTION 'regional app task billing interval cannot be rewritten' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END
$function$;
CREATE TRIGGER regional_app_task_billing_guard
 BEFORE INSERT OR UPDATE OR DELETE ON regional_app_task_billing_intervals
 FOR EACH ROW EXECUTE FUNCTION guard_regional_app_task_billing_history();
CREATE TRIGGER regional_app_task_billing_truncate_guard
 BEFORE TRUNCATE ON regional_app_task_billing_intervals
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_app_task_billing_history_truncate();
CREATE TRIGGER regional_app_task_billing_start_guard
 BEFORE INSERT OR UPDATE OR DELETE ON regional_app_task_billing_history_start
 FOR EACH ROW EXECUTE FUNCTION refuse_regional_app_task_billing_history_truncate();
CREATE TRIGGER regional_app_task_billing_start_truncate_guard
 BEFORE TRUNCATE ON regional_app_task_billing_history_start
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_app_task_billing_history_truncate();

CREATE FUNCTION capture_regional_app_task_billing_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
 old_billable boolean := false;
 new_billable boolean := false;
 same_identity boolean := false;
 same_run boolean := false;
 changed_at timestamptz;
 closed_at timestamptz;
 old_app uuid;
 new_app uuid;
BEGIN
 IF TG_OP <> 'INSERT' THEN old_billable := OLD.status IN ('restoring','running'); END IF;
 IF TG_OP <> 'DELETE' THEN new_billable := NEW.status IN ('restoring','running'); END IF;
 IF TG_OP='UPDATE' THEN
  same_identity := (NEW.app_id,NEW.id,NEW.deployment_id) IS NOT DISTINCT FROM (OLD.app_id,OLD.id,OLD.deployment_id);
  same_run := same_identity AND (NEW.lease_token,NEW.lease_owner) IS NOT DISTINCT FROM (OLD.lease_token,OLD.lease_owner);
 END IF;
 IF old_billable=new_billable AND (NOT old_billable OR same_run) THEN RETURN NULL; END IF;
 IF old_billable THEN old_app:=OLD.app_id; END IF;
 IF new_billable THEN new_app:=NEW.app_id; END IF;
 IF LEAST(old_app,new_app) IS NOT NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||LEAST(old_app,new_app)::text,0));
 END IF;
 IF GREATEST(old_app,new_app) IS DISTINCT FROM LEAST(old_app,new_app) THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||GREATEST(old_app,new_app)::text,0));
 END IF;
 changed_at:=clock_timestamp();
 IF old_billable THEN
  -- A normal terminal CAS retains a reported finish before lease expiry.
  -- Sweeps, replacement, deletion and bare requeue cannot prove closure.
  IF TG_OP='UPDATE' AND same_identity AND NEW.status IN ('succeeded','failed','timed_out','cancelled')
   AND NEW.finished_at IS NOT NULL AND NEW.finished_at < OLD.lease_expires_at
   AND COALESCE(NEW.failure_code,'') <> 'lease_expired' THEN
   closed_at:=NEW.finished_at;
  END IF;
  UPDATE regional_app_task_billing_intervals
   SET ended_at=CASE WHEN closed_at >= started_at THEN closed_at ELSE NULL END,
       unknown_since=CASE WHEN closed_at >= started_at THEN NULL ELSE GREATEST(started_at,changed_at) END
   WHERE task_id=OLD.id AND ended_at IS NULL AND unknown_since IS NULL;
 END IF;
 IF new_billable THEN
  INSERT INTO regional_app_task_billing_intervals
   (app_id,task_id,deployment_id,run_id,lease_owner,started_at,recorded_at,complete_start)
  VALUES (NEW.app_id,NEW.id,NEW.deployment_id,NEW.lease_token,NEW.lease_owner,NEW.updated_at,changed_at,true);
 END IF;
 RETURN NULL;
END
$function$;
CREATE TRIGGER zz_app_tasks_regional_billing_history
 AFTER INSERT OR UPDATE OR DELETE ON app_tasks
 FOR EACH ROW EXECUTE FUNCTION capture_regional_app_task_billing_history();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE app_tasks IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE regional_app_task_billing_intervals IN ACCESS EXCLUSIVE MODE;
DO $function$
BEGIN
 IF EXISTS (SELECT 1 FROM regional_app_task_billing_intervals) THEN
  RAISE EXCEPTION 'cannot discard retained regional app task billing history' USING ERRCODE='23514';
 END IF;
END
$function$;
DROP TRIGGER zz_app_tasks_regional_billing_history ON app_tasks;
DROP TABLE regional_app_task_billing_intervals;
DROP TABLE regional_app_task_billing_history_start;
DROP FUNCTION capture_regional_app_task_billing_history();
DROP FUNCTION guard_regional_app_task_billing_history();
DROP FUNCTION refuse_regional_app_task_billing_history_truncate();
-- +goose StatementEnd
