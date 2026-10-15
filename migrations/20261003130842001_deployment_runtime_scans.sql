-- +goose Up
-- adr: 435. Private composed-runtime facts; no approval or native authority.
CREATE TABLE deployment_runtime_scans (
 id uuid PRIMARY KEY,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 scanned_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at>scanned_at AND expires_at<=scanned_at+interval '5 minutes'),
 UNIQUE(id,deployment_id),
 CHECK ((input_snapshot->>'deployment_id'=deployment_id::text AND input_snapshot->>'format'='gregale.runtime-artifact-input.v1') IS TRUE),
 CHECK ((input_snapshot->>'status' IN ('complete','failed') AND input_snapshot->'facts'->>'version'='1'
  AND input_snapshot->'facts'->>'input_hash' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->'facts'->>'sources_hash' ~ '^[a-f0-9]{64}$') IS TRUE)
);
CREATE TABLE deployment_runtime_scan_current (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 scan_id uuid NOT NULL,
 FOREIGN KEY(scan_id,deployment_id) REFERENCES deployment_runtime_scans(id,deployment_id) ON DELETE CASCADE
);
-- +goose StatementBegin
CREATE FUNCTION deployment_runtime_scan_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND current_setting('gregale.runtime_scan_insert',true)=NEW.id::text THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'runtime scan evidence is immutable/private' USING ERRCODE='23514',CONSTRAINT='deployment_runtime_scan_immutable';
END;
$$;
CREATE FUNCTION deployment_runtime_scan_current_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 IF TG_OP<>'DELETE' AND current_setting('gregale.runtime_scan_insert',true)=NEW.scan_id::text
  AND (TG_OP='INSERT' OR NEW.deployment_id=OLD.deployment_id) THEN RETURN NEW; END IF;
 RAISE EXCEPTION 'runtime scan selection is private' USING ERRCODE='23514',CONSTRAINT='deployment_runtime_scan_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER runtime_scan_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_runtime_scans
 FOR EACH ROW EXECUTE FUNCTION deployment_runtime_scan_guard();
CREATE TRIGGER runtime_scan_current_private_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_runtime_scan_current
 FOR EACH ROW EXECUTE FUNCTION deployment_runtime_scan_current_guard();
CREATE TRIGGER application_standard_runtime_scan_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_runtime_scans
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
CREATE TRIGGER application_standard_runtime_scan_current_child_guard BEFORE INSERT OR UPDATE OR DELETE ON deployment_runtime_scan_current
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();

-- +goose Down
DROP TABLE deployment_runtime_scan_current;
DROP TABLE deployment_runtime_scans;
DROP FUNCTION deployment_runtime_scan_current_guard();
DROP FUNCTION deployment_runtime_scan_guard();
