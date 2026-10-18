-- +goose Up
-- adr: 387. This retains registry-source evidence, not converted-rootfs or
-- native authority. Only the private store rechecks cryptography/current keys.
CREATE TABLE deployment_registry_verifications (
 id uuid PRIMARY KEY,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 app_id uuid NOT NULL,
 account_id uuid NOT NULL,
 workload_name text NOT NULL CHECK (workload_name='' OR (workload_name <> 'main' AND workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$')),
 input_snapshot jsonb NOT NULL CHECK (jsonb_typeof(input_snapshot)='object'),
 input_hash text NOT NULL CHECK (input_hash ~ '^[a-f0-9]{64}$'),
 payload bytea NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 65536),
 signature bytea NOT NULL CHECK (octet_length(signature) BETWEEN 1 AND 80),
 verified_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK (expires_at > verified_at AND expires_at <= verified_at + interval '24 hours'),
 CHECK (input_snapshot->>'deployment_id'=deployment_id::text AND input_snapshot->>'app_id'=app_id::text
   AND input_snapshot->>'account_id'=account_id::text AND input_snapshot->>'workload_name'=workload_name)
);
CREATE INDEX deployment_registry_verifications_latest ON deployment_registry_verifications(deployment_id,workload_name,verified_at DESC,id DESC);

-- No parent wait behind child locks: current ownership/reference and key are
-- read under nonwaiting parent and exclusive control/artifact fences. Existing
-- signer writers take a shared control fence, including rotations/deletions.
-- +goose StatementBegin
CREATE FUNCTION lock_deployment_registry_verification(application_id uuid, artifact_id uuid, owner_id uuid, workload text, publisher text)
RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE a apps%ROWTYPE; d deployments%ROWTYPE; image_ref text; key_der bytea; matches integer;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=artifact_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry artifact missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 SELECT * INTO a FROM apps WHERE id=application_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'registry application missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_missing';
 END IF;
 IF a.id IS DISTINCT FROM d.app_id OR a.account_id IS DISTINCT FROM owner_id OR a.status='deleted'
   OR d.status NOT IN ('pending','building','imaging','snapshotting','live','superseded') THEN
  RAISE EXCEPTION 'registry artifact scope changed' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.' || a.id::text,0))
   OR NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.artifact-children.' || d.id::text,0)) THEN
  RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
 END IF;
 IF workload='' AND d.kind='image' THEN
  image_ref := d.image_digest;
 ELSIF workload <> '' THEN
  SELECT count(*),min(s->>'image') INTO matches,image_ref FROM jsonb_array_elements(d.sidecars) s WHERE s->>'name'=workload;
  IF matches <> 1 THEN image_ref := NULL; END IF;
 END IF;
 IF image_ref IS NULL OR image_ref='' THEN
  RAISE EXCEPTION 'registry workload missing' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_stale';
 END IF;
 SELECT cosign_public_key INTO key_der FROM app_trusted_signers WHERE app_id=a.id AND signer_name=publisher AND account_id=a.account_id;
 RETURN jsonb_build_object('app_id',a.id::text,'account_id',a.account_id::text,'org_id',coalesce(a.org_id::text,''),
   'image_reference',image_ref,'key_der',coalesce(encode(key_der,'base64'),''));
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'registry verification inputs busy' USING ERRCODE='55P03',CONSTRAINT='deployment_registry_verification_busy';
END;
$$;
-- +goose StatementEnd

-- Protect against accidental raw insert/mutation by other store paths. The
-- DB writer is trusted; this guard is not a cryptographic database attestation.
-- Parent erasure cascades remain possible without retained customer evidence.
-- +goose StatementBegin
CREATE FUNCTION deployment_registry_verification_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF current_setting('gregale.registry-verification-insert',true) IS DISTINCT FROM NEW.id::text THEN
   RAISE EXCEPTION 'registry verification must use private store' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'registry verification is immutable' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER deployment_registry_verification_immutable BEFORE INSERT OR UPDATE OR DELETE
 ON deployment_registry_verifications FOR EACH ROW EXECUTE FUNCTION deployment_registry_verification_guard();
CREATE TRIGGER application_standard_registry_artifact_input_guard BEFORE INSERT OR UPDATE OR DELETE
 ON deployment_registry_verifications FOR EACH ROW EXECUTE FUNCTION application_standard_artifact_child_guard();

-- +goose Down
DROP TABLE deployment_registry_verifications;
DROP FUNCTION deployment_registry_verification_guard();
DROP FUNCTION lock_deployment_registry_verification(uuid,uuid,uuid,text,text);
