-- filename: 20261004141805537_application_standard_egress_protocol.sql
-- ADR-435: match runtimeadmission.ArtifactProtocolVersion (2); preserve prior bytes.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_egress_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
  OR actual->'identity'->>'ProtocolVersion'<>'2' OR actual->'identity'->>'Incarnation'=''
  OR NEW.receipt<>jsonb_build_object('identity',actual->'identity','app_id',actual->>'app_id',
   'revision',actual->'policy'->'revision','policy_hash',actual->>'policy_hash') THEN
  RAISE EXCEPTION 'standard egress process or inputs changed' USING ERRCODE='40001',CONSTRAINT='application_standard_egress_current';
 END IF;
 NEW.observed_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_egress_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
-- +goose StatementEnd
