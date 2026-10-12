-- +goose Up
-- +goose StatementBegin
LOCK TABLE apps,deployments IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE regional_runtime_floor_history_start (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 started_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO regional_runtime_floor_history_start(singleton) VALUES(true);
CREATE TABLE regional_runtime_floor_intervals (
 id bigserial PRIMARY KEY,
 app_id uuid NOT NULL CHECK(app_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 object_id uuid NOT NULL CHECK(object_id <> '00000000-0000-0000-0000-000000000000'::uuid),
 kind text NOT NULL CHECK(kind IN ('app','deployment')),
 started_at timestamptz NOT NULL,
 ended_at timestamptz,
 state_claims jsonb NOT NULL CHECK(jsonb_typeof(state_claims)='object'),
 CHECK(ended_at IS NULL OR ended_at>=started_at),
 CHECK(kind<>'app' OR object_id=app_id)
);
CREATE UNIQUE INDEX regional_runtime_floor_open_idx
 ON regional_runtime_floor_intervals(kind,object_id) WHERE ended_at IS NULL;
CREATE INDEX regional_runtime_floor_app_page_idx ON regional_runtime_floor_intervals(app_id,id);

CREATE FUNCTION regional_runtime_app_floor_claims(a apps)
RETURNS jsonb LANGUAGE sql IMMUTABLE AS $function$
 SELECT jsonb_build_object('RAMMB',a.ram_mb,'MinInstances',a.min_instances,'Status',a.status,'ScalingPolicy',jsonb_build_object('min_instances',COALESCE(a.scaling_policy->'min_instances','0'::jsonb),'timezone',COALESCE(a.scaling_policy->'timezone','""'::jsonb),'schedules',COALESCE(a.scaling_policy->'schedules','null'::jsonb)))
$function$;
CREATE FUNCTION regional_runtime_deployment_floor_claims(d deployments)
RETURNS jsonb LANGUAGE sql IMMUTABLE AS $function$
 SELECT jsonb_build_object('MinInstances',d.min_instances,'Status',d.status,'Scope',d.scope,'CreatedAt',to_char(d.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'))
$function$;
INSERT INTO regional_runtime_floor_intervals(app_id,object_id,kind,started_at,state_claims)
SELECT a.id,a.id,'app',s.started_at,regional_runtime_app_floor_claims(a)
FROM apps a CROSS JOIN regional_runtime_floor_history_start s;
INSERT INTO regional_runtime_floor_intervals(app_id,object_id,kind,started_at,state_claims)
SELECT d.app_id,d.id,'deployment',s.started_at,regional_runtime_deployment_floor_claims(d)
FROM deployments d CROSS JOIN regional_runtime_floor_history_start s;

CREATE FUNCTION refuse_regional_runtime_floor_history_truncate()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 RAISE EXCEPTION 'regional floor history is retained' USING ERRCODE='23514';
END
$function$;
CREATE FUNCTION guard_regional_runtime_floor_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
BEGIN
 IF TG_OP='DELETE' OR pg_trigger_depth()<2 THEN
  RAISE EXCEPTION 'regional floor history is intent owned' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' AND (
  (NEW.id,NEW.app_id,NEW.object_id,NEW.kind,NEW.started_at,NEW.state_claims) IS DISTINCT FROM
  (OLD.id,OLD.app_id,OLD.object_id,OLD.kind,OLD.started_at,OLD.state_claims)
  OR OLD.ended_at IS NOT NULL OR NEW.ended_at IS NULL) THEN
  RAISE EXCEPTION 'regional floor interval cannot be rewritten' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END
$function$;
CREATE TRIGGER regional_runtime_floor_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_floor_intervals
 FOR EACH ROW EXECUTE FUNCTION guard_regional_runtime_floor_history();
CREATE TRIGGER regional_runtime_floor_truncate_guard BEFORE TRUNCATE ON regional_runtime_floor_intervals
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_floor_history_truncate();
CREATE TRIGGER regional_runtime_floor_start_guard BEFORE INSERT OR UPDATE OR DELETE ON regional_runtime_floor_history_start
 FOR EACH ROW EXECUTE FUNCTION refuse_regional_runtime_floor_history_truncate();
CREATE TRIGGER regional_runtime_floor_start_truncate_guard BEFORE TRUNCATE ON regional_runtime_floor_history_start
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_floor_history_truncate();

CREATE FUNCTION capture_regional_runtime_floor_history()
RETURNS trigger LANGUAGE plpgsql AS $function$
DECLARE
 kind_arg text;
 old_app uuid;
 new_app uuid;
 old_object uuid;
 new_object uuid;
 old_claims jsonb;
 new_claims jsonb;
 changed_at timestamptz;
BEGIN
 IF TG_TABLE_NAME='apps' THEN
  kind_arg:='app';
  IF TG_OP<>'INSERT' THEN old_app:=OLD.id;old_object:=OLD.id;old_claims:=regional_runtime_app_floor_claims(OLD); END IF;
  IF TG_OP<>'DELETE' THEN new_app:=NEW.id;new_object:=NEW.id;new_claims:=regional_runtime_app_floor_claims(NEW); END IF;
 ELSE
  kind_arg:='deployment';
  IF TG_OP<>'INSERT' THEN old_app:=OLD.app_id;old_object:=OLD.id;old_claims:=regional_runtime_deployment_floor_claims(OLD); END IF;
  IF TG_OP<>'DELETE' THEN new_app:=NEW.app_id;new_object:=NEW.id;new_claims:=regional_runtime_deployment_floor_claims(NEW); END IF;
 END IF;
 IF TG_OP='UPDATE' AND (old_app,old_object,old_claims) IS NOT DISTINCT FROM (new_app,new_object,new_claims) THEN RETURN NULL; END IF;
 IF LEAST(old_app,new_app) IS NOT NULL THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||LEAST(old_app,new_app)::text,0));
 END IF;
 IF GREATEST(old_app,new_app) IS DISTINCT FROM LEAST(old_app,new_app) THEN
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale-regional-runtime-quota:'||GREATEST(old_app,new_app)::text,0));
 END IF;
 changed_at:=clock_timestamp();
 IF old_object IS NOT NULL THEN
  UPDATE regional_runtime_floor_intervals SET ended_at=GREATEST(started_at,changed_at)
   WHERE kind=kind_arg AND object_id=old_object AND ended_at IS NULL;
 END IF;
 IF new_object IS NOT NULL THEN
  INSERT INTO regional_runtime_floor_intervals(app_id,object_id,kind,started_at,state_claims)
   VALUES(new_app,new_object,kind_arg,changed_at,new_claims);
 END IF;
 RETURN NULL;
END
$function$;
CREATE TRIGGER zz_apps_regional_runtime_floor_history AFTER INSERT OR UPDATE OR DELETE ON apps
 FOR EACH ROW EXECUTE FUNCTION capture_regional_runtime_floor_history();
CREATE TRIGGER zz_deployments_regional_runtime_floor_history AFTER INSERT OR UPDATE OR DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION capture_regional_runtime_floor_history();
-- Source truncation bypasses row capture and cannot silently end promises.
CREATE TRIGGER apps_regional_floor_truncate_guard BEFORE TRUNCATE ON apps
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_floor_history_truncate();
CREATE TRIGGER deployments_regional_floor_truncate_guard BEFORE TRUNCATE ON deployments
 FOR EACH STATEMENT EXECUTE FUNCTION refuse_regional_runtime_floor_history_truncate();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE apps,deployments IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE regional_runtime_floor_intervals IN ACCESS EXCLUSIVE MODE;
DO $function$
BEGIN
 IF EXISTS(SELECT 1 FROM regional_runtime_floor_intervals) THEN
  RAISE EXCEPTION 'cannot discard retained regional floor history' USING ERRCODE='23514';
 END IF;
END
$function$;
DROP TRIGGER apps_regional_floor_truncate_guard ON apps;
DROP TRIGGER deployments_regional_floor_truncate_guard ON deployments;
DROP TRIGGER zz_apps_regional_runtime_floor_history ON apps;
DROP TRIGGER zz_deployments_regional_runtime_floor_history ON deployments;
DROP TABLE regional_runtime_floor_intervals;
DROP TABLE regional_runtime_floor_history_start;
DROP FUNCTION capture_regional_runtime_floor_history();
DROP FUNCTION guard_regional_runtime_floor_history();
DROP FUNCTION refuse_regional_runtime_floor_history_truncate();
DROP FUNCTION regional_runtime_app_floor_claims(apps);
DROP FUNCTION regional_runtime_deployment_floor_claims(deployments);
-- +goose StatementEnd
