-- filename: 20261005180954298_application_standard_snapshot_restore_publication_fence.sql
-- adr: 593. Keep catalog lookup unambiguous and fence every first runtime tuple.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_lock_snapshot_restore(b jsonb, input jsonb) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE restore_token_text text:=coalesce(b->>'snapshot_capture_token',''); evidence text:=coalesce(b->>'snapshot_evidence_hash','');
 selected record;
BEGIN
 IF restore_token_text='' AND evidence='' THEN RETURN; END IF;
 IF (b->>'protocol_version'='2' AND jsonb_typeof(b->'snapshot_capture_token')='string'
  AND restore_token_text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND restore_token_text<>'00000000-0000-0000-0000-000000000000' AND jsonb_typeof(b->'snapshot_evidence_hash')='string'
  AND evidence ~ '^[0-9a-f]{64}$') IS NOT TRUE THEN
  RAISE EXCEPTION 'restore binding is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT c.grant_data,c.acknowledgment,c.input_snapshot,to_jsonb(s) AS snapshot_row INTO selected FROM application_standard_snapshot_captures c
 JOIN snapshots s ON s.application_standard_capture_token=c.token
 WHERE c.token=restore_token_text::uuid AND c.account_id::text=b->>'account_id' AND c.app_id::text=b->>'app_id'
  AND c.deployment_id::text=b->>'deployment_id' AND s.deployment_id=c.deployment_id
  AND c.acknowledgment IS NOT NULL AND c.received_at IS NOT NULL AND NOT s.stale AND NOT s.delete_pending
 ORDER BY s.created_at DESC,s.id DESC LIMIT 1 FOR SHARE OF c,s NOWAIT;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'restore catalog is unavailable' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF application_standard_restore_catalog_matches(b,input,selected.grant_data,selected.acknowledgment,selected.input_snapshot,selected.snapshot_row) IS NOT TRUE
  OR (b->>'expires_at_unix_nano')::bigint<=(extract(epoch FROM clock_timestamp())*1000000000)::bigint THEN
  RAISE EXCEPTION 'restore catalog or current input is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'restore catalog is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_restore_publication_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE b jsonb; locked jsonb;
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','warm','migrating')
  OR NEW.application_standard_boot_token IS NULL
  OR (NEW.state NOT IN ('running','warm','migrating') AND coalesce(NEW.netns,'')=''
   AND NEW.host_ip IS NULL AND coalesce(NEW.guest_uid,0)=0)
  OR (NEW.application_standard_boot_token IS NOT DISTINCT FROM OLD.application_standard_boot_token AND OLD.state NOT IN ('waking','cold_booting')) THEN RETURN NEW; END IF;
 SELECT binding INTO b FROM instance_application_standard_boots
  WHERE token=NEW.application_standard_boot_token AND instance_id=NEW.id FOR SHARE NOWAIT;
 IF coalesce(b->>'snapshot_capture_token','')='' AND coalesce(b->>'snapshot_evidence_hash','')='' THEN RETURN NEW; END IF;
 locked:=application_standard_lock_native_boot(OLD.id,OLD.state);
 PERFORM application_standard_lock_snapshot_restore(b,locked->'input_snapshot');
 RETURN NEW;
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'restore publication is busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
DROP TRIGGER IF EXISTS application_standard_restore_publication_guard ON instances;
CREATE TRIGGER application_standard_restore_publication_guard BEFORE UPDATE ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_restore_publication_guard();
-- +goose StatementEnd

-- +goose Down
-- Retain fail-closed restore publication authority on binary rollback.
