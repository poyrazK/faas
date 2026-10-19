-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS project_migration_contracts (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 attempt_id text NOT NULL CHECK(attempt_id ~ '^[a-f0-9]{32}$'),
 app_id uuid NOT NULL,
 workload_attempt_id text NOT NULL CHECK(workload_attempt_id ~ '^[a-f0-9]{32}$'),
 expansion_task_id uuid,
 prepared_lease_token uuid,
 CHECK(prepared_lease_token IS NULL OR task_id IS NOT NULL),
 runtime_fingerprint text CHECK(runtime_fingerprint ~ '^[a-f0-9]{64}$'),
 CHECK((expansion_task_id IS NULL)=(runtime_fingerprint IS NULL)),
 command text[] NOT NULL CHECK(cardinality(command)=1),
 command_hash text NOT NULL CHECK(command_hash ~ '^[a-f0-9]{64}$'),
 task_id uuid UNIQUE,
 request_id text CHECK(request_id ~ '^[a-f0-9]{32}$'),
 generation bigint CHECK(generation>0),
 release_id uuid,
 config_version bigint CHECK(config_version>=0),
 config_hash text CHECK(config_hash ~ '^[a-f0-9]{64}$'),
 retired_deployment_ids uuid[] NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(created_at)),
 PRIMARY KEY(account_id,attempt_id),
 CHECK((task_id IS NULL AND request_id IS NULL AND generation IS NULL AND release_id IS NULL AND config_version IS NULL AND config_hash IS NULL AND cardinality(retired_deployment_ids)=0)
 OR (task_id IS NOT NULL AND runtime_fingerprint IS NOT NULL AND expansion_task_id IS NOT NULL AND request_id IS NOT NULL AND generation IS NOT NULL AND release_id IS NOT NULL AND config_version IS NOT NULL AND config_hash IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS project_migration_contracts_project ON project_migration_contracts(project_id);
CREATE INDEX IF NOT EXISTS project_migration_contracts_retired ON project_migration_contracts USING gin(retired_deployment_ids);
CREATE OR REPLACE VIEW project_migration_retired_deployments AS
SELECT DISTINCT unnest(retired_deployment_ids) AS deployment_id FROM project_migration_contracts;
CREATE OR REPLACE VIEW project_migration_pending_contracts AS
SELECT c.* FROM project_migration_contracts c LEFT JOIN app_tasks t ON t.id=c.task_id
WHERE c.task_id IS NOT NULL AND (t.id IS NULL OR t.status IN ('queued','restoring','running'));
CREATE OR REPLACE VIEW project_migration_task_admission AS
SELECT t.id FROM app_tasks t JOIN apps a ON a.id=t.app_id
WHERE NOT EXISTS(SELECT 1 FROM project_migration_contracts f WHERE f.retired_deployment_ids @> ARRAY[t.deployment_id])
AND NOT EXISTS(SELECT 1 FROM project_migration_pending_contracts c WHERE c.project_id=a.project_id AND c.task_id<>t.id)
AND NOT EXISTS(SELECT 1 FROM project_migration_contracts c WHERE c.task_id=t.id AND (
 NOT EXISTS(SELECT 1 FROM project_release_sets r WHERE r.id=c.release_id AND r.project_id=c.project_id AND r.active)
 OR NOT ((c.config_version=0 AND NOT EXISTS(SELECT 1 FROM project_environment_config_versions empty_head WHERE empty_head.project_id=c.project_id AND empty_head.environment_slug='production')) OR EXISTS(SELECT 1 FROM project_environment_config_versions v WHERE v.project_id=c.project_id AND v.environment_slug='production'
 AND v.version=c.config_version AND v.config_hash=c.config_hash AND NOT EXISTS(SELECT 1 FROM project_environment_config_versions newer
 WHERE newer.project_id=c.project_id AND newer.environment_slug='production' AND newer.version>v.version)))));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_migration_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE project uuid; deployment uuid;
BEGIN
 IF TG_TABLE_NAME='deployments' THEN deployment:=NEW.id; ELSE deployment:=NEW.deployment_id; END IF;
 SELECT project_id INTO project FROM apps WHERE id=NEW.app_id;
 IF project IS NULL OR NOT EXISTS(SELECT 1 FROM project_migration_contracts c WHERE c.project_id=project) THEN RETURN NEW; END IF;
 PERFORM 1 FROM projects WHERE id=project FOR UPDATE;
 IF EXISTS(SELECT 1 FROM project_migration_contracts f WHERE f.retired_deployment_ids @> ARRAY[deployment]) THEN
  IF TG_TABLE_NAME='instances' THEN
   IF NEW.state IN ('parked','stopped','failed') THEN RETURN NEW; END IF;
  ELSIF TG_TABLE_NAME='deployments' THEN
   IF NEW.status<>'live' THEN RETURN NEW; END IF;
   IF TG_OP='UPDATE' AND OLD.status='live' AND OLD.traffic_percent=0 AND NEW.traffic_percent=0 THEN RETURN NEW; END IF;
  ELSIF TG_TABLE_NAME='app_tasks' THEN
   IF NEW.status NOT IN ('queued','restoring','running') THEN RETURN NEW; END IF;
  END IF;
  RAISE EXCEPTION 'deployment retired by schema contract' USING ERRCODE='23514';
 END IF;
 IF TG_TABLE_NAME='app_tasks' THEN
 IF NEW.status='running' AND EXISTS(SELECT 1 FROM project_migration_contracts c WHERE c.task_id=NEW.id AND (c.prepared_lease_token IS NULL OR c.prepared_lease_token<>NEW.lease_token)) THEN
  RAISE EXCEPTION 'cleanup runtime has not been attested for this lease' USING ERRCODE='23514';
 END IF;
 IF NEW.status='running' AND NEW.kind='release' AND EXISTS(SELECT 1 FROM project_migration_contracts c JOIN deployment_submission_receipts ds ON ds.app_id=c.app_id AND ds.attempt_id=c.workload_attempt_id WHERE ds.deployment_id=NEW.deployment_id AND (c.expansion_task_id IS NULL OR c.expansion_task_id<>NEW.id OR c.runtime_fingerprint IS NULL)) THEN
  RAISE EXCEPTION 'expansion runtime has not been attested' USING ERRCODE='23514';
 END IF;
 IF NEW.status IN ('restoring','running') AND NOT EXISTS(SELECT 1 FROM project_migration_task_admission admission WHERE admission.id=NEW.id) THEN
  RAISE EXCEPTION 'schema contract task admission fenced' USING ERRCODE='23514';
 END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- Retirement must serialize with contract admission before the native runtime
-- guard takes its NOWAIT app fence; the aa_ prefix makes this trigger fire first.
CREATE OR REPLACE TRIGGER aa_project_migration_instance_fence BEFORE INSERT OR UPDATE OF state,deployment_id ON instances FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_deployment_fence BEFORE INSERT OR UPDATE OF status,traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_task_fence BEFORE INSERT OR UPDATE OF status ON app_tasks FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_revision_fence BEFORE INSERT OR UPDATE ON deployment_revision_pins FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_operation_fence BEFORE INSERT OR UPDATE ON customer_operation_code_pins FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_workflow_fence BEFORE INSERT OR UPDATE ON workflow_code_pins FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_worker_fence BEFORE INSERT OR UPDATE ON project_worker_release_pins FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
CREATE OR REPLACE TRIGGER project_migration_member_fence BEFORE INSERT OR UPDATE ON project_release_members FOR EACH ROW EXECUTE FUNCTION guard_project_migration_retirement();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_migration_publication() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM project_migration_contracts c WHERE c.project_id=NEW.project_id) THEN RETURN NEW; END IF;
 PERFORM 1 FROM projects WHERE id=NEW.project_id FOR UPDATE;
 IF TG_TABLE_NAME='project_release_sets' THEN
  IF NEW.active AND EXISTS(SELECT 1 FROM project_migration_pending_contracts c WHERE c.project_id=NEW.project_id AND c.release_id<>NEW.id) THEN
   RAISE EXCEPTION 'schema contract prevents publication' USING ERRCODE='23514';
  END IF;
 ELSE
  IF NEW.environment_slug='production' AND EXISTS(SELECT 1 FROM project_migration_pending_contracts c WHERE c.project_id=NEW.project_id) THEN
   RAISE EXCEPTION 'schema contract prevents configuration change' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE TRIGGER project_migration_publication_fence BEFORE INSERT OR UPDATE OF active ON project_release_sets FOR EACH ROW EXECUTE FUNCTION guard_project_migration_publication();
CREATE OR REPLACE TRIGGER project_migration_configuration_fence BEFORE INSERT ON project_environment_config_versions FOR EACH ROW EXECUTE FUNCTION guard_project_migration_publication();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_migration_contracts WHERE task_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cannot remove retirement fences after schema contract admission' USING ERRCODE='23514';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER aa_project_migration_instance_fence ON instances;
DROP TRIGGER project_migration_deployment_fence ON deployments;
DROP TRIGGER project_migration_task_fence ON app_tasks;
DROP TRIGGER project_migration_revision_fence ON deployment_revision_pins;
DROP TRIGGER project_migration_operation_fence ON customer_operation_code_pins;
DROP TRIGGER project_migration_workflow_fence ON workflow_code_pins;
DROP TRIGGER project_migration_worker_fence ON project_worker_release_pins;
DROP TRIGGER project_migration_member_fence ON project_release_members;
DROP TRIGGER project_migration_publication_fence ON project_release_sets;
DROP TRIGGER project_migration_configuration_fence ON project_environment_config_versions;
DROP FUNCTION guard_project_migration_retirement();
DROP FUNCTION guard_project_migration_publication();

DROP VIEW project_migration_task_admission;
DROP VIEW project_migration_pending_contracts;
DROP VIEW project_migration_retired_deployments;
DROP TABLE project_migration_contracts;
