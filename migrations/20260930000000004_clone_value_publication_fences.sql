-- +goose Up
-- ADR-375: publication holds the app rows before reading target values. Every
-- configuration writer, including maintenance and binding reconciliation,
-- takes the same lock so a completed proof has one commit order with edits.
-- Runtime delivery observations do not participate in the configuration hash.
-- +goose StatementBegin
CREATE FUNCTION serialize_clone_value_publication() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[];
BEGIN
    IF TG_OP = 'INSERT' THEN
        app_ids := ARRAY[NEW.app_id];
    ELSIF TG_OP = 'DELETE' THEN
        app_ids := ARRAY[OLD.app_id];
    ELSE
        app_ids := ARRAY[OLD.app_id, NEW.app_id];
    END IF;
    PERFORM id FROM apps WHERE id = ANY(app_ids) ORDER BY id FOR NO KEY UPDATE;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER clone_variable_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF app_id, scope, key, value ON app_envs
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
CREATE TRIGGER clone_secret_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF
app_id, scope, key, ciphertext, kid, value_hash, secret_class, secret_version,
managed_postgres_binding_id, managed_object_storage_credential_id,
managed_credential_ref, managed_credential_generation ON app_secrets
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER clone_secret_publication_fence ON app_secrets;
DROP TRIGGER clone_variable_publication_fence ON app_envs;
DROP FUNCTION serialize_clone_value_publication();
