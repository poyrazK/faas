-- filename: 20261004164819478_application_standard_log_health.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE application_standard_log_health (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
 drain_id uuid NOT NULL REFERENCES app_log_drains(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL CHECK(session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 generation bigint NOT NULL CHECK(generation>0),
 binding jsonb NOT NULL CHECK(jsonb_typeof(binding)='object'),
 event_revision bigint NOT NULL CHECK(event_revision>0),
 status text NOT NULL CHECK(status IN ('unknown','healthy','degraded')),
 reason text NOT NULL CHECK(reason IN ('idle','delivered','retrying','delivery_failed','queue_fault','records_lost','source_gap','stream_unavailable','reporter_exhausted')),
 source_instance_id uuid CHECK(source_instance_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 sequence bigint NOT NULL CHECK(sequence>=0),
 event_at timestamptz NOT NULL CHECK(event_at>'epoch'::timestamptz),
 observed_at timestamptz NOT NULL CHECK(observed_at>'epoch'::timestamptz),
 PRIMARY KEY(app_id,drain_id,node_id),
 CHECK(event_revision<9223372036854775807 OR (status='degraded' AND reason='reporter_exhausted')),
 CHECK((status='healthy' AND reason='delivered' AND source_instance_id IS NOT NULL AND sequence>0)
  OR (status='unknown' AND reason='idle' AND source_instance_id IS NULL AND sequence=0)
  OR (status='degraded' AND reason NOT IN ('idle','delivered') AND source_instance_id IS NULL AND sequence=0))
);
CREATE FUNCTION application_standard_log_health_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'logging health projection is busy' USING ERRCODE='55P03';
 END IF;
 PERFORM h.app_id FROM application_standard_log_health h
  WHERE h.app_id=NEW.app_id AND h.drain_id=NEW.drain_id AND h.node_id=NEW.node_id FOR UPDATE NOWAIT;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
  JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM d.id FROM app_log_drains d JOIN application_standard_control_bindings b ON b.app_id=d.app_id AND b.field='log_destinations' AND b.physical_id=d.id::text
  JOIN application_standard_log_destinations r ON r.id=b.resource_id
  WHERE d.app_id=NEW.app_id AND d.id=NEW.drain_id FOR SHARE OF d,b,r NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id FOR SHARE OF c,n NOWAIT;
 IF NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
  WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation
   AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging health consumer changed' USING ERRCODE='55000';
 END IF;
 IF NEW.status='healthy' THEN
  PERFORM i.id FROM instances i WHERE i.id=NEW.source_instance_id AND i.app_id=NEW.app_id FOR SHARE NOWAIT;
  IF NOT FOUND THEN RAISE EXCEPTION 'logging health source changed' USING ERRCODE='40001'; END IF;
 END IF;
 actual:=application_standard_log_binding(NEW.app_id,NEW.drain_id);
 IF actual IS NULL OR actual<>NEW.binding OR actual->>'org_id'<>NEW.org_id::text THEN
  RAISE EXCEPTION 'logging health binding changed' USING ERRCODE='40001';
 END IF;
 NEW.observed_at:=clock_timestamp();
 NEW.event_at:=NEW.observed_at;
 IF TG_OP='UPDATE' THEN
  IF (NEW.app_id,NEW.drain_id,NEW.node_id) IS DISTINCT FROM (OLD.app_id,OLD.drain_id,OLD.node_id) THEN
   RAISE EXCEPTION 'logging health identity changed' USING ERRCODE='40001';
  END IF;
  IF NEW.binding=OLD.binding AND NEW.session_id=OLD.session_id AND NEW.generation=OLD.generation THEN
   IF NEW.event_revision<OLD.event_revision OR (NEW.event_revision=OLD.event_revision AND
    (NEW.status,NEW.reason,NEW.source_instance_id,NEW.sequence) IS DISTINCT FROM (OLD.status,OLD.reason,OLD.source_instance_id,OLD.sequence)) THEN
    RAISE EXCEPTION 'logging health event superseded' USING ERRCODE='GS001';
   END IF;
   IF NEW.event_revision=OLD.event_revision THEN NEW.event_at:=OLD.event_at; END IF;
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_health_current BEFORE INSERT OR UPDATE ON application_standard_log_health
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_health_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE application_standard_log_health;
DROP FUNCTION application_standard_log_health_guard();
-- +goose StatementEnd
