-- +goose Up
-- +goose StatementBegin
-- Establish coverage only after all earlier instance writers have committed.
LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE regional_runtime_billing_history_start (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO regional_runtime_billing_history_start(singleton) VALUES (true);

CREATE TABLE regional_runtime_billing_intervals (
 id bigserial PRIMARY KEY,
 app_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 deployment_id uuid,
 node_id uuid,
 run_id uuid,
 started_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 complete_start boolean NOT NULL,
 ended_at timestamptz,
 unknown_since timestamptz,
 CHECK (app_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 CHECK (instance_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 CHECK (deployment_id IS NULL OR deployment_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 CHECK (node_id IS NULL OR node_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 CHECK (run_id IS NULL OR run_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 CHECK (ended_at IS NULL OR ended_at >= started_at),
 CHECK (unknown_since IS NULL OR unknown_since >= started_at),
 CHECK (ended_at IS NULL OR unknown_since IS NULL)
);
CREATE UNIQUE INDEX regional_runtime_billing_open_idx
 ON regional_runtime_billing_intervals(instance_id)
 WHERE ended_at IS NULL AND unknown_since IS NULL;
CREATE INDEX regional_runtime_billing_app_page_idx
 ON regional_runtime_billing_intervals(app_id,id);

-- Existing runs are not a source of complete historical evidence.
INSERT INTO regional_runtime_billing_intervals
 (app_id,instance_id,deployment_id,node_id,run_id,started_at,recorded_at,complete_start)
SELECT i.app_id,i.id,NULLIF(i.deployment_id,'00000000-0000-0000-0000-000000000000'::uuid),
 NULLIF(i.node_id,'00000000-0000-0000-0000-000000000000'::uuid),
 NULLIF(i.wake_id,'00000000-0000-0000-0000-000000000000'::uuid),s.started_at,s.started_at,false
FROM instances i CROSS JOIN regional_runtime_billing_history_start s
WHERE i.app_id IS NOT NULL AND i.app_id <> '00000000-0000-0000-0000-000000000000'::uuid AND i.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
 AND COALESCE(i.mode,'normal') <> 'mirror';

CREATE FUNCTION refuse_regional_runtime_billing_history_truncate()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 RAISE EXCEPTION 'regional runtime billing history is retained' USING ERRCODE='23514';
END
$function$;

CREATE FUNCTION guard_regional_runtime_billing_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 IF TG_OP='DELETE' OR pg_trigger_depth() < 2 THEN
  RAISE EXCEPTION 'regional runtime billing history is lifecycle owned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (
  (NEW.id,NEW.app_id,NEW.instance_id,NEW.deployment_id,NEW.node_id,NEW.run_id,
   NEW.started_at,NEW.recorded_at,NEW.complete_start) IS DISTINCT FROM
  (OLD.id,OLD.app_id,OLD.instance_id,OLD.deployment_id,OLD.node_id,OLD.run_id,
   OLD.started_at,OLD.recorded_at,OLD.complete_start)
  OR OLD.ended_at IS NOT NULL OR OLD.unknown_since IS NOT NULL
  OR (NEW.ended_at IS NULL AND NEW.unknown_since IS NULL)) THEN
  RAISE EXCEPTION 'regional runtime billing interval cannot be rewritten' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END
$function$;
CREATE TRIGGER regional_runtime_billing_guard
 BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_billing_intervals
 FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_billing_history();
CREATE TRIGGER regional_runtime_billing_truncate_guard
 BEFORE TRUNCATE ON regional_runtime_billing_intervals
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_billing_history_truncate();
CREATE TRIGGER regional_runtime_billing_start_guard
 BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_billing_history_start
 FOR EACH ROW EXECUTE FUNCTION refuse_regional_runtime_billing_history_truncate();
CREATE TRIGGER regional_runtime_billing_start_truncate_guard
 BEFORE TRUNCATE ON regional_runtime_billing_history_start
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_billing_history_truncate();

CREATE FUNCTION capture_regional_runtime_billing_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
 old_billable boolean := false;
 new_billable boolean := false;
 same_run boolean := false;
 changed_at timestamptz;
 old_app uuid;
 new_app uuid;
BEGIN
 IF TG_OP <> 'INSERT' THEN
  old_billable := OLD.app_id IS NOT NULL AND OLD.app_id <> '00000000-0000-0000-0000-000000000000'::uuid AND OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND COALESCE(OLD.mode,'normal') <> 'mirror';
 END IF;
 IF TG_OP <> 'DELETE' THEN
  new_billable := NEW.app_id IS NOT NULL AND NEW.app_id <> '00000000-0000-0000-0000-000000000000'::uuid AND NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
   AND COALESCE(NEW.mode,'normal') <> 'mirror';
 END IF;
 IF TG_OP='UPDATE' THEN
  same_run := (NEW.app_id,NEW.id,NEW.deployment_id,NEW.node_id,NEW.wake_id) IS NOT DISTINCT FROM
   (OLD.app_id,OLD.id,OLD.deployment_id,OLD.node_id,OLD.wake_id);
 END IF;
 IF old_billable=new_billable AND (NOT old_billable OR same_run) THEN RETURN NULL; END IF;
 IF old_billable THEN old_app:=OLD.app_id; END IF;
 IF new_billable THEN new_app:=NEW.app_id; END IF;
 -- Serialize actual journal mutations with issuance and future minute sealing.
 -- Both roots use the quota journal's canonical order on reparenting.
 IF LEAST(old_app,new_app) IS NOT NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||LEAST(old_app,new_app)::text,0));
 END IF;
 IF GREATEST(old_app,new_app) IS DISTINCT FROM LEAST(old_app,new_app) THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||GREATEST(old_app,new_app)::text,0));
 END IF;
 changed_at:=clock_timestamp();
 IF old_billable AND (TG_OP='DELETE' OR NOT same_run OR NOT new_billable) THEN
  IF TG_OP='DELETE' OR NOT same_run THEN
   UPDATE regional_runtime_billing_intervals SET unknown_since=GREATEST(started_at,changed_at)
    WHERE instance_id=OLD.id AND ended_at IS NULL AND unknown_since IS NULL;
  ELSE
   UPDATE regional_runtime_billing_intervals SET ended_at=GREATEST(started_at,changed_at)
    WHERE instance_id=OLD.id AND ended_at IS NULL AND unknown_since IS NULL;
  END IF;
 END IF;
 IF new_billable AND (NOT old_billable OR NOT same_run) THEN
  INSERT INTO regional_runtime_billing_intervals
   (app_id,instance_id,deployment_id,node_id,run_id,started_at,recorded_at,complete_start)
  VALUES (NEW.app_id,NEW.id,NULLIF(NEW.deployment_id,'00000000-0000-0000-0000-000000000000'::uuid),
   NULLIF(NEW.node_id,'00000000-0000-0000-0000-000000000000'::uuid),
   NULLIF(NEW.wake_id,'00000000-0000-0000-0000-000000000000'::uuid),
   CASE WHEN TG_OP='INSERT' THEN COALESCE(NEW.started_at,changed_at) ELSE changed_at END,changed_at,true);
 END IF;
 RETURN NULL;
END
$function$;
CREATE TRIGGER zz_instances_regional_runtime_billing_history
 AFTER INSERT OR UPDATE OR DELETE ON instances
 FOR EACH ROW EXECUTE FUNCTION capture_regional_runtime_billing_history();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE instances IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE regional_runtime_billing_intervals IN ACCESS EXCLUSIVE MODE;
DO $function$
BEGIN
 IF EXISTS (SELECT 1 FROM regional_runtime_billing_intervals) THEN
  RAISE EXCEPTION 'cannot discard retained regional runtime billing history' USING ERRCODE='23514';
 END IF;
END
$function$;
DROP TRIGGER zz_instances_regional_runtime_billing_history ON instances;
DROP TABLE regional_runtime_billing_intervals;
DROP TABLE regional_runtime_billing_history_start;
DROP FUNCTION capture_regional_runtime_billing_history();
DROP FUNCTION guard_regional_runtime_billing_history();
DROP FUNCTION refuse_regional_runtime_billing_history_truncate();
-- +goose StatementEnd
