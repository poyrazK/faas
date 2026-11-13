-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Ignore foreign reservations left by older workers, even before they retry.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION data_pipeline_setup_owner_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='accounts' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE account_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the account';
  END IF;
 ELSIF TG_TABLE_NAME='data_pipeline_connections' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE source_connection_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the source';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE database_id=OLD.id AND account_id=OLD.account_id AND state IN ('working','blocked')) THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the mirror';
  END IF;
  IF NEW.desired_generation<>OLD.desired_generation OR NEW.state IN ('deleting','deleted') THEN
   RAISE EXCEPTION 'active data pipeline setup owns the mirror lifecycle';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- Serialize admission with destination lifecycle changes. This also protects
-- direct catalog writers and workers from the previous release.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION data_pipeline_setup_target_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 destination_owner uuid;
 destination_state text;
 private_destination boolean;
 requested text;
BEGIN
 IF NEW.state NOT IN ('working','blocked') THEN RETURN NEW; END IF;
 requested:=coalesce(NEW.request->>'DatabaseID','');
 IF requested<>'' AND requested::uuid<>NEW.database_id THEN
  RAISE EXCEPTION 'pipeline setup destination does not match its request' USING ERRCODE='23503';
 END IF;
 SELECT account_id,state,environment_clone_operation_id IS NOT NULL
 INTO destination_owner,destination_state,private_destination
 FROM managed_postgres_databases WHERE id=NEW.database_id FOR SHARE;
 IF NOT FOUND THEN
  IF requested<>'' THEN
   RAISE EXCEPTION 'pipeline setup destination is unavailable' USING ERRCODE='23503';
  END IF;
  RETURN NEW;
 END IF;
 IF destination_owner<>NEW.account_id OR private_destination THEN
  RAISE EXCEPTION 'pipeline setup destination is unavailable' USING ERRCODE='23503';
 END IF;
 IF destination_state IN ('deleting','deleted') THEN
  RAISE EXCEPTION 'pipeline setup destination is retiring';
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS data_pipeline_setup_target_guard ON data_pipeline_setups;
CREATE TRIGGER data_pipeline_setup_target_guard
 BEFORE INSERT OR UPDATE OF account_id,database_id,request,state ON data_pipeline_setups
 FOR EACH ROW EXECUTE FUNCTION data_pipeline_setup_target_guard();
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER data_pipeline_setup_target_guard ON data_pipeline_setups;
DROP FUNCTION data_pipeline_setup_target_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION data_pipeline_setup_owner_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='accounts' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE account_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the account';
  END IF;
 ELSIF TG_TABLE_NAME='data_pipeline_connections' THEN
  IF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE source_connection_id=OLD.id AND state IN ('working','blocked')) THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the source';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM data_pipeline_setups WHERE database_id=OLD.id AND state IN ('working','blocked')) THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'stop data pipeline setups before deleting the mirror';
  END IF;
  IF NEW.desired_generation<>OLD.desired_generation OR NEW.state IN ('deleting','deleted') THEN
   RAISE EXCEPTION 'active data pipeline setup owns the mirror lifecycle';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
