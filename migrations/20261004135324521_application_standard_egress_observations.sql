-- filename: 20261004135324521_application_standard_egress_observations.sql
-- ADR-435: current native process and complete projection acknowledgment.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_egress_policy_hash(a apps) RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT encode(sha256(convert_to('gregale.egress-policy.v1'||E'\n'||a.id::text||E'\n'||
 a.egress_allowlist_revision::text||E'\n'||
 coalesce((SELECT string_agg(c.key,',' ORDER BY c.key COLLATE "C") FROM
  (SELECT DISTINCT encode(substring(inet_send(prefix) from 5),'hex')||'/'||masklen(prefix)::text AS key FROM unnest(a.egress_allowlist) prefix) c),'')||E'\n'||
 coalesce((SELECT string_agg(p.port::text,',' ORDER BY p.port) FROM (SELECT DISTINCT port FROM unnest(a.egress_ports) port) p),''),'UTF8')),'hex');
$$;
CREATE FUNCTION application_standard_egress_target(app uuid,node uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('org_id',a.org_id::text,'app_id',a.id::text,
 'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
 'identity',jsonb_build_object('ProtocolVersion',coalesce(n.vmmd_admission_protocol,0),'NodeID',n.id::text,'Incarnation',coalesce(n.vmmd_incarnation::text,'')),
 'policy',jsonb_build_object('app_id',a.id::text,'revision',a.egress_allowlist_revision,'allowlist',to_jsonb(a.egress_allowlist::text[]),'ports',to_jsonb(a.egress_ports)),
 'policy_hash',application_standard_egress_policy_hash(a))
 FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id JOIN compute_nodes n ON n.id=node
 WHERE a.id=app AND a.status='active' AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND e.effective_hash~'^[a-f0-9]{64}$' AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND n.active AND n.role IS DISTINCT FROM 'control-plane'
 AND EXISTS(SELECT 1 FROM instances i WHERE i.app_id=a.id AND i.node_id=n.id
  AND i.state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining'));
$$;
CREATE TABLE application_standard_egress_observations (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 target jsonb NOT NULL CHECK(jsonb_typeof(target)='object'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(observed_at>'epoch'::timestamptz),
 PRIMARY KEY(app_id,node_id)
);
CREATE FUNCTION application_standard_egress_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual jsonb;
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||NEW.app_id::text,0)) THEN
  RAISE EXCEPTION 'standard egress inputs are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id WHERE a.id=NEW.app_id FOR SHARE OF a,o,acct,e NOWAIT;
 PERFORM id FROM compute_nodes WHERE id=NEW.node_id FOR SHARE NOWAIT;
 PERFORM id FROM instances WHERE app_id=NEW.app_id AND node_id=NEW.node_id
  AND state IN ('waking','cold_booting','running','snapshotting','migrating','warm','draining') FOR SHARE NOWAIT;
 actual:=application_standard_egress_target(NEW.app_id,NEW.node_id);
 IF actual IS NULL OR actual<>NEW.target OR actual->>'org_id'<>NEW.org_id::text
  OR actual->'identity'->>'ProtocolVersion'<>'3' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_egress_current BEFORE INSERT OR UPDATE ON application_standard_egress_observations
 FOR EACH ROW EXECUTE FUNCTION application_standard_egress_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE application_standard_egress_observations;
DROP FUNCTION application_standard_egress_guard();
DROP FUNCTION application_standard_egress_target(uuid,uuid);
DROP FUNCTION application_standard_egress_policy_hash(apps);
-- +goose StatementEnd
