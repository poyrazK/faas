-- +goose Up
-- adr: 387. PostgreSQL custom GUC names require identifier components.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION deployment_registry_verification_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF current_setting('gregale.registry_verification_insert',true) IS DISTINCT FROM NEW.id::text THEN
   RAISE EXCEPTION 'registry verification must use private store' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN RETURN OLD; END IF;
 RAISE EXCEPTION 'registry verification is immutable' USING ERRCODE='23514',CONSTRAINT='deployment_registry_verification_immutable';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION deployment_registry_verification_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
