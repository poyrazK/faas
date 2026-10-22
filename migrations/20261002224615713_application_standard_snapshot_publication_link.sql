-- filename: 20261002224615713_application_standard_snapshot_publication_link.sql

-- +goose Up
-- +goose StatementBegin
-- adr: 431. Cache rows retain historical capture identity, never restore authority.
ALTER TABLE snapshots ADD COLUMN application_standard_capture_token uuid
 REFERENCES application_standard_snapshot_captures(token) ON DELETE CASCADE;
CREATE INDEX snapshots_application_standard_capture_idx ON snapshots(application_standard_capture_token)
 WHERE application_standard_capture_token IS NOT NULL;
COMMENT ON COLUMN snapshots.application_standard_capture_token IS
 'Immutable historical capture reference. A fresh restore grant and current artifact approval are still required.';

CREATE FUNCTION application_standard_snapshot_catalog_matches(s snapshots, c application_standard_snapshot_captures)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE g jsonb; a jsonb;
BEGIN
 g:=c.grant_data; a:=c.acknowledgment;
 RETURN coalesce(c.received_at IS NOT NULL AND a IS NOT NULL
  AND c.deployment_id=s.deployment_id AND c.memory_key=s.storage_key AND g->>'memory_key'=s.storage_key
  AND g->>'vmstate_key'=left(s.storage_key,length(s.storage_key)-3)||'vmstate'
  AND g->>'private_drive_key'=left(s.storage_key,length(s.storage_key)-3)||'drive'
  AND g->>'mode' IN ('warm','park') AND s.tier=CASE WHEN g->>'mode'='warm' THEN 'warm' ELSE 'init' END
  AND g->>'fc_version'=s.fc_version AND s.stored_bytes>=0
  AND (a->'capture'->'memory'->>'bytes')::bigint=s.mem_bytes
  AND (a->'capture'->'vmstate'->>'bytes')::bigint=s.disk_bytes
  AND application_standard_snapshot_acknowledgment_valid(a,g,(a->>'completed_at_unix_nano')::bigint),false);
EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN RETURN false;
END;
$$;

-- Do not upgrade incomplete or mismatched history into a receipt. Serialize the
-- additive backfill with catalog writers and the ALTER TABLE lock on snapshots.
LOCK TABLE application_standard_snapshot_captures IN SHARE ROW EXCLUSIVE MODE;
UPDATE snapshots s SET application_standard_capture_token=c.token
FROM application_standard_snapshot_captures c JOIN deployments d ON d.id=c.deployment_id
 JOIN apps app ON app.id=d.app_id AND app.id=c.app_id AND app.account_id=c.account_id
WHERE application_standard_snapshot_catalog_matches(s,c);
UPDATE snapshots s SET stale=true
WHERE s.application_standard_capture_token IS NULL
 AND EXISTS(SELECT 1 FROM application_standard_snapshot_captures c WHERE c.memory_key=s.storage_key);

CREATE FUNCTION application_standard_snapshot_publication_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE c application_standard_snapshot_captures;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.application_standard_capture_token IS DISTINCT FROM OLD.application_standard_capture_token
   OR (OLD.application_standard_capture_token IS NOT NULL AND
    (to_jsonb(NEW)-ARRAY['stale','delete_pending','stored_bytes']) IS DISTINCT FROM
    (to_jsonb(OLD)-ARRAY['stale','delete_pending','stored_bytes'])) THEN
   RAISE EXCEPTION 'snapshot capture association is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
  IF (to_jsonb(NEW)-ARRAY['stale','delete_pending','stored_bytes']) IS NOT DISTINCT FROM
   (to_jsonb(OLD)-ARRAY['stale','delete_pending','stored_bytes']) THEN RETURN NEW; END IF;
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.storage_key,43120261002));
 IF NEW.application_standard_capture_token IS NULL THEN
  IF EXISTS(SELECT 1 FROM application_standard_snapshot_captures WHERE memory_key=NEW.storage_key) THEN
   RAISE EXCEPTION 'snapshot requires its catalog reference' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
  END IF;
  RETURN NEW; -- Unknown legacy cache rows never become measured capture proof.
 END IF;
 SELECT * INTO c FROM application_standard_snapshot_captures
 WHERE token=NEW.application_standard_capture_token FOR KEY SHARE;
 IF NOT FOUND OR NOT application_standard_snapshot_catalog_matches(NEW,c)
  OR NOT EXISTS(SELECT 1 FROM deployments d JOIN apps a ON a.id=d.app_id
   WHERE d.id=NEW.deployment_id AND a.id=c.app_id AND a.account_id=c.account_id) THEN
  RAISE EXCEPTION 'snapshot does not match published capture' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_identity';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_snapshot_publication_guard BEFORE INSERT OR UPDATE ON snapshots
 FOR EACH ROW EXECUTE FUNCTION application_standard_snapshot_publication_guard();

CREATE FUNCTION application_standard_snapshot_namespace_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended(NEW.memory_key,43120261002));
 IF EXISTS(SELECT 1 FROM snapshots WHERE storage_key=NEW.memory_key) THEN
  RAISE EXCEPTION 'snapshot key was published before its grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_snapshot_namespace_guard BEFORE INSERT ON application_standard_snapshot_captures
 FOR EACH ROW EXECUTE FUNCTION application_standard_snapshot_namespace_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER application_standard_snapshot_namespace_guard ON application_standard_snapshot_captures;
DROP FUNCTION application_standard_snapshot_namespace_guard();
DROP TRIGGER application_standard_snapshot_publication_guard ON snapshots;
DROP FUNCTION application_standard_snapshot_publication_guard();
DROP FUNCTION application_standard_snapshot_catalog_matches(snapshots,application_standard_snapshot_captures);
ALTER TABLE snapshots DROP COLUMN application_standard_capture_token;
-- +goose StatementEnd
