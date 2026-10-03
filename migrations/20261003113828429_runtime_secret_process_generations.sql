-- adr:438
-- +goose Up
CREATE TABLE app_secret_runtime_processes (
    instance_id uuid NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    workload_name text NOT NULL DEFAULT '' CHECK (workload_name = '' OR workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    generation text NOT NULL DEFAULT '' CHECK (generation = '' OR generation ~ '^[0-9a-f]{32}$'),
    active boolean NOT NULL DEFAULT false,
    started_at timestamptz,
    PRIMARY KEY (instance_id, workload_name),
    CONSTRAINT app_secret_runtime_process_state_chk CHECK (
        (generation = '' AND NOT active AND started_at IS NULL)
        OR (generation <> '' AND started_at IS NOT NULL)
    )
);
CREATE INDEX app_secret_runtime_process_app_idx ON app_secret_runtime_processes(app_id);
ALTER TABLE app_secret_runtime_reload_observations
    ADD COLUMN application_ack_generation text NOT NULL DEFAULT ''
    CHECK (application_ack_generation = '' OR application_ack_generation ~ '^[0-9a-f]{32}$');
CREATE TRIGGER binding_promotion_revision
    AFTER INSERT OR DELETE OR UPDATE ON app_secret_runtime_processes
    FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,instance_id,workload_name,generation,active,started_at');

DROP TRIGGER binding_promotion_revision ON app_secret_runtime_reload_observations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_secret_runtime_reload_observations
    FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code,application_ack_generation');

-- +goose Down
DROP TRIGGER binding_promotion_revision ON app_secret_runtime_reload_observations;
CREATE TRIGGER binding_promotion_revision AFTER INSERT OR DELETE OR UPDATE ON app_secret_runtime_reload_observations
    FOR EACH ROW EXECUTE FUNCTION capture_binding_promotion_revision('app_id,scope,key,instance_id,workload_name,secret_version,projection,signal,observed_at,error_code,application_ack_version,application_ack_status,application_ack_at,application_ack_error_code');
ALTER TABLE app_secret_runtime_reload_observations DROP COLUMN application_ack_generation;
DROP TABLE app_secret_runtime_processes;
