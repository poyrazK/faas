-- +goose Up
-- ADR-531: a paused VM retains its captured configuration, even after restart.
-- Old VMs have no proof and must be retired rather than adopting current values.
CREATE TABLE IF NOT EXISTS runtime_instance_config_proofs (
    instance_id uuid PRIMARY KEY REFERENCES instances(id) ON DELETE CASCADE,
    wake_id uuid NOT NULL CHECK (wake_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    node_id uuid NOT NULL CHECK (node_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    environment_id uuid CHECK (environment_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    scope text NOT NULL CHECK (scope='default' OR scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
    secret_fingerprint text NOT NULL CHECK (secret_fingerprint ~ '^[a-f0-9]{64}$'),
    config_fingerprint text NOT NULL CHECK (config_fingerprint ~ '^[a-f0-9]{64}$')
);

-- Publication reads child sets while holding their parent, including empty
-- sets. It never waits on value-row locks after taking the app/deployment lock.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION serialize_runtime_deployment_configuration() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE deployment_ids uuid[];
BEGIN
    IF TG_OP='INSERT' THEN deployment_ids:=ARRAY[NEW.deployment_id];
    ELSIF TG_OP='DELETE' THEN deployment_ids:=ARRAY[OLD.deployment_id];
    ELSE deployment_ids:=ARRAY[OLD.deployment_id,NEW.deployment_id]; END IF;
    PERFORM id FROM deployments WHERE id=ANY(deployment_ids) ORDER BY id FOR NO KEY UPDATE;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE TRIGGER runtime_sidecar_layers_configuration_fence BEFORE INSERT OR UPDATE OR DELETE ON deployment_sidecar_layers
FOR EACH ROW EXECUTE FUNCTION serialize_runtime_deployment_configuration();
CREATE OR REPLACE TRIGGER runtime_sidecar_signals_configuration_fence BEFORE INSERT OR UPDATE OR DELETE ON deployment_sidecar_secret_reload_signals
FOR EACH ROW EXECUTE FUNCTION serialize_runtime_deployment_configuration();
CREATE OR REPLACE TRIGGER runtime_deployment_specs_configuration_fence BEFORE INSERT OR UPDATE OR DELETE ON project_environment_workload_deployment_specs
FOR EACH ROW EXECUTE FUNCTION serialize_runtime_deployment_configuration();
CREATE OR REPLACE TRIGGER runtime_environment_owners_configuration_fence BEFORE INSERT OR UPDATE OR DELETE ON deployment_runtime_environment_owners
FOR EACH ROW EXECUTE FUNCTION serialize_runtime_deployment_configuration();
-- +goose StatementEnd

DROP TRIGGER IF EXISTS clone_variable_publication_fence ON app_envs;
CREATE OR REPLACE TRIGGER clone_variable_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF account_id,app_id,scope,key,value,created_at ON app_envs
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
DROP TRIGGER IF EXISTS clone_secret_publication_fence ON app_secrets;
CREATE OR REPLACE TRIGGER clone_secret_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF
account_id,app_id,scope,key,ciphertext,kid,value_hash,secret_class,secret_version,delivery_version,created_at,
managed_postgres_binding_id,managed_object_storage_credential_id,managed_credential_ref,managed_credential_generation ON app_secrets
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();

-- +goose Down
DROP TRIGGER runtime_environment_owners_configuration_fence ON deployment_runtime_environment_owners;
DROP TRIGGER runtime_deployment_specs_configuration_fence ON project_environment_workload_deployment_specs;
DROP TRIGGER runtime_sidecar_signals_configuration_fence ON deployment_sidecar_secret_reload_signals;
DROP TRIGGER runtime_sidecar_layers_configuration_fence ON deployment_sidecar_layers;
DROP FUNCTION serialize_runtime_deployment_configuration();
DROP TABLE runtime_instance_config_proofs;
DROP TRIGGER clone_variable_publication_fence ON app_envs;
CREATE TRIGGER clone_variable_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF app_id,scope,key,value ON app_envs
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
DROP TRIGGER clone_secret_publication_fence ON app_secrets;
CREATE TRIGGER clone_secret_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF
app_id,scope,key,ciphertext,kid,value_hash,secret_class,secret_version,
managed_postgres_binding_id,managed_object_storage_credential_id,managed_credential_ref,managed_credential_generation ON app_secrets
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
