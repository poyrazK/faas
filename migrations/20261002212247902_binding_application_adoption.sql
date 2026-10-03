-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_sidecar_binding_promotion_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 target uuid;
BEGIN
 IF TG_OP='UPDATE' AND OLD.deployment_id=NEW.deployment_id AND OLD.sidecar_name=NEW.sidecar_name AND OLD.signal=NEW.signal THEN RETURN NEW; END IF;
 FOR target IN SELECT DISTINCT app_id FROM deployments
  WHERE id IN (NULLIF(old_row->>'deployment_id','')::uuid,NULLIF(new_row->>'deployment_id','')::uuid)
  ORDER BY app_id LOOP
  PERFORM bump_binding_promotion_revision(target);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS binding_promotion_revision ON app_secret_runtime_reload_observations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON app_secret_runtime_reload_observations
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code');
DROP TRIGGER IF EXISTS binding_promotion_revision ON deployment_sidecar_secret_reload_signals;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON deployment_sidecar_secret_reload_signals
 FOR EACH ROW EXECUTE FUNCTION capture_sidecar_binding_promotion_revision('deployment_id,sidecar_name,signal');
DROP TRIGGER IF EXISTS binding_promotion_revision ON deployments;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state,override_env_secrets,sidecars,secret_reload_signal');
-- Invalidate any approvals observed before this migration installed its fences.
SELECT bump_binding_promotion_revision(id) FROM apps ORDER BY id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER binding_promotion_revision ON app_secret_runtime_reload_observations;
DROP TRIGGER binding_promotion_revision ON deployment_sidecar_secret_reload_signals;
DROP FUNCTION capture_sidecar_binding_promotion_revision();
DROP TRIGGER binding_promotion_revision ON deployments;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR UPDATE OR DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('id,app_id,status,scope,rootfs_key,image_digest,traffic_percent,canary_total_steps,rollout_state');
SELECT bump_binding_promotion_revision(id) FROM apps ORDER BY id;
-- +goose StatementEnd
