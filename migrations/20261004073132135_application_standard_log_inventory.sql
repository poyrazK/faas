-- filename: 20261004073132135_application_standard_log_inventory.sql
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_log_inventory(app uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,'account_id',a.account_id::text,
  'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,'drains',
  coalesce((SELECT jsonb_agg(jsonb_build_object('drain_id',d.id::text,'config_hash',application_standard_log_drain_hash(d)) ORDER BY d.id)
   FROM app_log_drains d WHERE d.app_id=a.id AND d.enabled),'[]'::jsonb))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp());
$$;
CREATE TABLE application_standard_log_consumers (
 node_id uuid PRIMARY KEY REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL CHECK(session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 generation bigint NOT NULL CHECK(generation>0),
 registered_at timestamptz NOT NULL CHECK(registered_at>'epoch'::timestamptz)
);
CREATE FUNCTION application_standard_log_consumer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=NEW.node_id AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging consumer node is unavailable' USING ERRCODE='40001';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.generation<>1 THEN RAISE EXCEPTION 'invalid logging consumer generation' USING ERRCODE='40001'; END IF;
  NEW.registered_at:=clock_timestamp();
 ELSE
  IF NEW.node_id<>OLD.node_id OR NEW.generation<>OLD.generation+(CASE WHEN NEW.session_id=OLD.session_id THEN 0 ELSE 1 END) THEN
   RAISE EXCEPTION 'logging consumer generation changed' USING ERRCODE='40001';
  END IF;
  NEW.registered_at:=CASE WHEN NEW.session_id=OLD.session_id THEN OLD.registered_at ELSE clock_timestamp() END;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_consumer_current BEFORE INSERT OR UPDATE ON application_standard_log_consumers
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_guard();
CREATE TABLE application_standard_log_inventories (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL CHECK(session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 generation bigint NOT NULL CHECK(generation>0),
 inventory jsonb NOT NULL CHECK(jsonb_typeof(inventory)='object'),
 observed_at timestamptz NOT NULL CHECK(observed_at>'epoch'::timestamptz),
 PRIMARY KEY(app_id,node_id)
);
CREATE FUNCTION application_standard_log_inventory_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual jsonb;
BEGIN
 -- This nonwaiting exclusive fence also covers drain inserts/deletes, not just existing rows.
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging inventory is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
 WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 NEW.observed_at:=clock_timestamp();
 actual:=application_standard_log_inventory(NEW.app_id);
 IF actual IS NULL OR actual<>NEW.inventory OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'application standard logging inventory changed' USING ERRCODE='40001';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'application standard logging consumer changed' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_inventory_current BEFORE INSERT OR UPDATE ON application_standard_log_inventories
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_inventory_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE application_standard_log_inventories;
DROP FUNCTION application_standard_log_inventory_guard();
DROP TABLE application_standard_log_consumers;
DROP FUNCTION application_standard_log_consumer_guard();
DROP FUNCTION application_standard_log_inventory(uuid);
-- +goose StatementEnd
