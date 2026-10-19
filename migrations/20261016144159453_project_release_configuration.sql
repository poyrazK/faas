-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_release_env_snapshots (
 deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
 values_json jsonb NOT NULL CHECK (jsonb_typeof(values_json)='array')
);
CREATE TABLE IF NOT EXISTS project_release_config_intents (
 account_id uuid NOT NULL,
 attempt_id text NOT NULL,
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload)='object'),
 PRIMARY KEY(account_id,attempt_id),
 FOREIGN KEY(account_id,attempt_id) REFERENCES project_submissions(account_id,attempt_id) ON DELETE CASCADE
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION protect_release_configuration_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  RAISE EXCEPTION 'release configuration snapshots are immutable' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='deployment_release_env_snapshots' THEN
  IF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id) THEN
   RAISE EXCEPTION 'release configuration snapshots follow deployment lifetime' USING ERRCODE='23514';
  END IF;
 ELSE
  IF EXISTS(SELECT 1 FROM project_submissions WHERE account_id=OLD.account_id AND attempt_id=OLD.attempt_id) THEN
   RAISE EXCEPTION 'release configuration snapshots follow submission lifetime' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER immutable_release_configuration BEFORE UPDATE OR DELETE ON deployment_release_env_snapshots
FOR EACH ROW EXECUTE FUNCTION protect_release_configuration_snapshot();
CREATE OR REPLACE TRIGGER immutable_release_configuration BEFORE UPDATE OR DELETE ON project_release_config_intents
FOR EACH ROW EXECUTE FUNCTION protect_release_configuration_snapshot();
CREATE OR REPLACE TRIGGER release_env_runtime_configuration BEFORE INSERT OR UPDATE OR DELETE ON deployment_release_env_snapshots
FOR EACH ROW EXECUTE FUNCTION serialize_runtime_deployment_configuration();

-- +goose Down
DROP TABLE project_release_config_intents;
DROP TABLE deployment_release_env_snapshots;
DROP FUNCTION protect_release_configuration_snapshot();
