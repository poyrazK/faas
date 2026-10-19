-- filename: 20261003172810200_application_standard_build_export_publications.sql
-- adr: 435. A private approved build publisher attests the complete local OCI
-- export. These records do not grant runtime, scan or observed adoption authority.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE build_export_publications (
 id uuid PRIMARY KEY,
 build_id uuid NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 app_id uuid NOT NULL,
 account_id uuid NOT NULL,
 input_snapshot jsonb NOT NULL CHECK(jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK(input_hash ~ '^[a-f0-9]{64}$'),
 payload bytea NOT NULL CHECK(octet_length(payload) BETWEEN 1 AND 8192),
 signature bytea NOT NULL CHECK(octet_length(signature) BETWEEN 1 AND 80),
 verified_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>verified_at AND expires_at<=verified_at+interval '24 hours'),
 CHECK((input_snapshot->'claims'->>'format'='gregale.build-export.v1'
  AND input_snapshot->'claims'->>'build_id'=build_id::text
  AND input_snapshot->'claims'->>'deployment_id'=deployment_id::text
  AND input_snapshot->'claims'->>'app_id'=app_id::text
  AND input_snapshot->'claims'->>'account_id'=account_id::text
  AND input_snapshot->'claims'->>'source_sha256' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->'claims'->>'export_digest' ~ '^sha256:[a-f0-9]{64}$'
  AND (input_snapshot->'claims'->>'export_bytes')::bigint BETWEEN 1 AND 17213423616
  AND input_snapshot->'proof'->>'publisher_key_sha256' ~ '^[a-f0-9]{64}$'
  AND input_snapshot->'proof'->>'payload_digest'='sha256:'||encode(sha256(payload),'hex')
  AND input_snapshot->'proof'->>'signature_digest'='sha256:'||encode(sha256(signature),'hex')) IS TRUE)
);
CREATE INDEX build_export_publications_latest ON build_export_publications(build_id,verified_at DESC,id DESC);

CREATE FUNCTION lock_build_export_publication(input jsonb,publisher text,fresh boolean) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE c jsonb:=input->'claims';a apps%ROWTYPE;d deployments%ROWTYPE;b builds%ROWTYPE;p build_provenance%ROWTYPE;key_der bytea;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=(c->>'deployment_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 SELECT * INTO a FROM apps WHERE id=(c->>'app_id')::uuid FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export owner missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 -- The build lock serializes publication for one exact claim. No parent wait.
 SELECT * INTO b FROM builds WHERE id=(c->>'build_id')::uuid FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'build export claim missing' USING ERRCODE='23514',CONSTRAINT='build_export_publication_missing';END IF;
 IF (a.id=d.app_id AND b.deployment_id=d.id AND a.account_id::text=c->>'account_id'
  AND coalesce(a.org_id::text,'')=c->>'org_id' AND coalesce(a.runtime,'')=c->>'runtime' AND a.status<>'deleted'
  AND b.started_at=(c->>'claim_started_at')::timestamptz
  AND d.kind IN ('tarball','dockerfile','github','preview')
  AND d.status IN ('pending','building','imaging','snapshotting','live','superseded')
  AND (coalesce(d.source_sha256,'')='' OR d.source_sha256=c->>'source_sha256')) IS NOT TRUE THEN
  RAISE EXCEPTION 'build export owner changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||a.id::text,0))
  OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.'||d.id::text,0)) THEN
  RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
 END IF;
 IF fresh OR b.status='succeeded' THEN
  SELECT * INTO p FROM build_provenance WHERE build_id=b.id FOR SHARE NOWAIT;
  IF (b.status='succeeded' AND p.started_at=b.started_at AND p.source_sha256=c->>'source_sha256'
   AND coalesce(p.builder_node_id,'')=c->>'builder_node_id') IS NOT TRUE THEN
   RAISE EXCEPTION 'build export completion changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
  END IF;
 ELSIF b.status<>'running' THEN
  RAISE EXCEPTION 'build export claim changed' USING ERRCODE='23514',CONSTRAINT='build_export_publication_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND account_id=a.account_id AND signer_name=publisher;
 RETURN jsonb_build_object('key_der',coalesce(encode(key_der,'base64'),''),'checked_at',clock_timestamp());
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'build export inputs busy' USING ERRCODE='55P03',CONSTRAINT='build_export_publication_busy';
END;
$$;

-- A private-write guard is not database cryptographic verification. The store
-- authenticates the retained bytes against the key read under the owner fence.
CREATE FUNCTION build_export_publication_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF current_setting('gregale.build_export_publication_insert',true) IS DISTINCT FROM NEW.id::text THEN
   RAISE EXCEPTION 'build export must use private store' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
  END IF;
  IF EXISTS(SELECT 1 FROM build_export_publications p WHERE p.build_id=NEW.build_id
   AND p.input_snapshot->'claims'->>'claim_started_at'=NEW.input_snapshot->'claims'->>'claim_started_at'
   AND p.input_snapshot->'claims' IS DISTINCT FROM NEW.input_snapshot->'claims') THEN
   RAISE EXCEPTION 'build export claim already published' USING ERRCODE='23514',CONSTRAINT='build_export_publication_conflict';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' AND (NOT EXISTS(SELECT 1 FROM builds WHERE id=OLD.build_id) OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id)) THEN RETURN OLD;END IF;
 RAISE EXCEPTION 'build export is immutable' USING ERRCODE='23514',CONSTRAINT='build_export_publication_immutable';
END;
$$;
CREATE TRIGGER build_export_publication_immutable BEFORE INSERT OR UPDATE OR DELETE ON build_export_publications
 FOR EACH ROW EXECUTE FUNCTION build_export_publication_guard();
CREATE TRIGGER application_standard_build_export_child_guard BEFORE INSERT OR UPDATE OR DELETE ON build_export_publications
 FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DROP TABLE build_export_publications;
DROP FUNCTION build_export_publication_guard();
DROP FUNCTION lock_build_export_publication(jsonb,text,boolean);
-- +goose StatementEnd
