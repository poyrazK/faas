-- filename: 20261004061225002_application_standard_log_delivery_observations.sql
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_log_drain_hash(d app_log_drains) RETURNS text
 LANGUAGE sql IMMUTABLE STRICT AS $$
 SELECT encode(sha256(convert_to('gregale.standard.log-drain.v1','UTF8')||decode('00','hex')
  ||convert_to(d.id::text,'UTF8')||decode('00','hex')
  ||convert_to(d.app_id::text,'UTF8')||decode('00','hex')
  ||convert_to(d.account_id::text,'UTF8')||decode('00','hex')
  ||convert_to(d.kind,'UTF8')||decode('00','hex')
  ||convert_to(d.target_url,'UTF8')||decode('00','hex')
  ||convert_to(d.enabled::text,'UTF8')||decode('00','hex')||coalesce(d.auth_header_sealed,''::bytea)),'hex');
$$;
CREATE FUNCTION application_standard_log_binding(app uuid,drain uuid) RETURNS jsonb
 LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('org_id',e.org_id::text,'app_id',a.id::text,'drain_id',d.id::text,
  'resource_id',r.id::text,'desired_revision',e.desired_revision,'effective_hash',e.effective_hash,
  'resource_config_hash',r.config_hash,'drain_config_hash',application_standard_log_drain_hash(d))
FROM app_log_drains d
 JOIN apps a ON a.id=d.app_id JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 JOIN app_application_standards e ON e.app_id=a.id
 JOIN application_standard_control_bindings b ON b.app_id=a.id AND b.field='log_destinations' AND b.physical_id=d.id::text
 JOIN application_standard_log_destinations r ON r.id=b.resource_id
 WHERE d.app_id=app AND d.id=drain AND d.enabled AND a.status='active' AND d.account_id=a.account_id
 AND o.status='active' AND NOT o.deleted_pending AND acct.status='active'
 AND e.org_id=a.org_id AND e.project_id IS NOT DISTINCT FROM a.project_id
 AND e.state IN ('persisted','observed') AND e.desired_revision=e.persisted_revision
 AND (e.exception_expires_at IS NULL OR e.exception_expires_at>clock_timestamp())
 AND jsonb_array_length(coalesce(e.effective->'sources'->'log_destinations','[]'::jsonb))>0
 AND (coalesce(e.effective->'values'->'log_destinations','[]'::jsonb) ? b.resource_id::text
      OR b.resource_id=ANY(e.additional_log_destinations))
 AND r.org_id=e.org_id;
$$;
CREATE TABLE application_standard_log_deliveries (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
 drain_id uuid NOT NULL REFERENCES app_log_drains(id) ON DELETE CASCADE,
 resource_id uuid NOT NULL REFERENCES application_standard_log_destinations(id) ON DELETE CASCADE,
 desired_revision bigint NOT NULL CHECK(desired_revision BETWEEN 1 AND 9007199254740991),
 effective_hash text NOT NULL CHECK(effective_hash ~ '^[a-f0-9]{64}$'),
 resource_config_hash text NOT NULL CHECK(resource_config_hash ~ '^[a-f0-9]{64}$'),
 drain_config_hash text NOT NULL CHECK(drain_config_hash ~ '^[a-f0-9]{64}$'),
 source_instance_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 observed_at timestamptz NOT NULL CHECK(observed_at>'epoch'::timestamptz),
 PRIMARY KEY(app_id,resource_id)
);
CREATE FUNCTION application_standard_log_delivery_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actual jsonb; expected jsonb;
BEGIN
 NEW.observed_at:=clock_timestamp();
 actual:=application_standard_log_binding(NEW.app_id,NEW.drain_id);
 expected:=jsonb_build_object('org_id',NEW.org_id::text,'app_id',NEW.app_id::text,'drain_id',NEW.drain_id::text,
  'resource_id',NEW.resource_id::text,'desired_revision',NEW.desired_revision,'effective_hash',NEW.effective_hash,
  'resource_config_hash',NEW.resource_config_hash,'drain_config_hash',NEW.drain_config_hash);
 IF actual IS NULL OR actual<>expected OR NOT EXISTS(SELECT 1 FROM instances i WHERE i.id=NEW.source_instance_id AND i.app_id=NEW.app_id) THEN
  RAISE EXCEPTION 'application standard logging projection changed' USING ERRCODE='40001';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_delivery_current BEFORE INSERT OR UPDATE ON application_standard_log_deliveries
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_delivery_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE application_standard_log_deliveries;
DROP FUNCTION application_standard_log_delivery_guard();
DROP FUNCTION application_standard_log_binding(uuid,uuid);
DROP FUNCTION application_standard_log_drain_hash(app_log_drains);
-- +goose StatementEnd
