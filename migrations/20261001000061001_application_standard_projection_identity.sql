-- +goose Up
-- Bindings and private restoration material are approval inputs too.
CREATE TRIGGER application_standard_backup_input_guard
BEFORE INSERT OR UPDATE OR DELETE ON application_standard_control_backups
FOR EACH ROW EXECUTE FUNCTION application_standard_control_input_guard();
CREATE TRIGGER application_standard_binding_input_guard
BEFORE INSERT OR UPDATE OR DELETE ON application_standard_control_bindings
FOR EACH ROW EXECUTE FUNCTION application_standard_control_input_guard();

-- A legacy child UPDATE must not move a managed control into an unmanaged
-- application and thereby bypass the projection check on the destination.
-- +goose StatementBegin
CREATE FUNCTION application_standard_managed_control_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.account_id IS DISTINCT FROM OLD.account_id
        OR to_jsonb(NEW)->TG_ARGV[1] IS DISTINCT FROM to_jsonb(OLD)->TG_ARGV[1])
       AND EXISTS (SELECT 1 FROM app_application_standards e WHERE e.app_id IN (OLD.app_id,NEW.app_id)
         AND coalesce(jsonb_array_length(e.effective->'sources'->TG_ARGV[0]),0) > 0) THEN
        RAISE EXCEPTION 'application standard manages this control'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_managed_control';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_y_drain_identity_guard
BEFORE UPDATE ON app_log_drains FOR EACH ROW
EXECUTE FUNCTION application_standard_managed_control_identity_guard('log_destinations','id');
CREATE TRIGGER application_standard_y_signer_identity_guard
BEFORE UPDATE ON app_trusted_signers FOR EACH ROW
EXECUTE FUNCTION application_standard_managed_control_identity_guard('trusted_publishers','signer_name');

-- +goose Down
DROP TRIGGER application_standard_y_signer_identity_guard ON app_trusted_signers;
DROP TRIGGER application_standard_y_drain_identity_guard ON app_log_drains;
DROP FUNCTION application_standard_managed_control_identity_guard();
DROP TRIGGER application_standard_binding_input_guard ON application_standard_control_bindings;
DROP TRIGGER application_standard_backup_input_guard ON application_standard_control_backups;
