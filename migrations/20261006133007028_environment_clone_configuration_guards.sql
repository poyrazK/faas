-- filename: 20261006133007028_environment_clone_configuration_guards.sql

-- +goose Up
-- ADR-590: the project guard serializes configuration mutations with a
-- durable capture hold. It is independent of worker lease expiry. Writers
-- retain a SHARE row lock through commit; acquisition takes UPDATE and reads
-- the live catalogue without locking configuration rows in the opposite order.
CREATE TABLE IF NOT EXISTS project_environment_clone_configuration_guards (
    project_id uuid PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    operation_id uuid REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'held')),
    source_environment text NOT NULL DEFAULT '',
    source_revision_hash text NOT NULL DEFAULT '',
    held_at timestamptz,
    CHECK ((state = 'open' AND operation_id IS NULL AND source_environment = '' AND source_revision_hash = '' AND held_at IS NULL)
        OR (state = 'held' AND operation_id IS NOT NULL AND source_environment <> '' AND source_revision_hash ~ '^[a-f0-9]{64}$' AND held_at IS NOT NULL))
);
INSERT INTO project_environment_clone_configuration_guards(project_id, account_id)
SELECT id, account_id FROM projects ON CONFLICT (project_id) DO NOTHING;

-- Acquisition briefly serializes with configuration writers across projects,
-- without holding their application rows. Updating this singleton also rejects
-- stale repeatable-read ownership lookups (including newly added bindings).
CREATE TABLE IF NOT EXISTS project_environment_clone_configuration_clock (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0)
);
INSERT INTO project_environment_clone_configuration_clock DEFAULT VALUES ON CONFLICT (singleton) DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION initialize_clone_configuration_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO project_environment_clone_configuration_guards(project_id, account_id)
    VALUES (NEW.id, NEW.account_id)
    ON CONFLICT (project_id) DO UPDATE SET account_id = EXCLUDED.account_id,
        generation = project_environment_clone_configuration_guards.generation + 1
    WHERE project_environment_clone_configuration_guards.state = 'open';
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS initialize_clone_configuration_guard ON projects;
CREATE TRIGGER initialize_clone_configuration_guard AFTER INSERT OR UPDATE OF account_id ON projects
FOR EACH ROW EXECUTE FUNCTION initialize_clone_configuration_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assert_clone_configuration_mutable(project uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    guard_state text;
BEGIN
    IF project IS NULL THEN RETURN; END IF;
    -- A locking read also prevents repeatable-read writers with a snapshot
    -- predating acquisition from bypassing the committed hold.
    SELECT state INTO guard_state FROM project_environment_clone_configuration_guards
    WHERE project_id = project FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'project configuration guard is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    IF guard_state <> 'open' THEN
        RAISE EXCEPTION 'source configuration is held for stage capture'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_write_fenced';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_clone_configuration_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    before_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
    after_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) ELSE '{}'::jsonb END;
    before_id uuid := nullif(before_row ->> TG_ARGV[1], '')::uuid;
    after_id uuid := nullif(after_row ->> TG_ARGV[1], '')::uuid;
    project uuid;
BEGIN
    PERFORM generation FROM project_environment_clone_configuration_clock WHERE singleton FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'configuration synchronization clock is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    FOR project IN
        SELECT DISTINCT p.project_id FROM (
            SELECT before_id AS project_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT after_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT a.project_id FROM apps a
                WHERE TG_ARGV[0] = 'app' AND a.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM deployments d JOIN apps a ON a.id = d.app_id
                WHERE TG_ARGV[0] = 'deployment' AND d.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM object_buckets b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'bucket' AND b.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM managed_postgres_bindings b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'database' AND b.database_id IN (before_id, after_id)
        ) p WHERE p.project_id IS NOT NULL ORDER BY p.project_id
    LOOP
        PERFORM assert_clone_configuration_mutable(project);
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- This covers the current typed root, including inserts into empty
-- collections and identities moved between projects. It is deliberately
-- project-wide, including other environments sharing App/configuration rows.
-- +goose StatementBegin
DO $$
DECLARE entry text;
DECLARE parts text[];
BEGIN
    FOREACH entry IN ARRAY ARRAY[
        'apps:project:project_id',
        'project_environments:project:project_id',
        'project_environment_config_versions:project:project_id',
        'project_environment_edge_policies:project:project_id',
        'project_environment_route_policies:project:project_id',
        'project_release_sets:project:project_id',
        'feature_flag_versions:project:project_id',
        'environment_git_sources:project:project_id',
        'app_envs:app:app_id',
        'app_secrets:app:app_id',
        'app_environment_secret_refs:app:app_id',
        'app_environment_secret_ref_suppressions:app:app_id',
        'app_environment_workload_intents:app:app_id',
        'project_environment_workload_heads:app:app_id',
        'project_environment_workload_specs:app:app_id',
        'deployments:app:app_id',
        'project_release_members:app:app_id',
        'app_work_policies:app:app_id',
        'event_subscription_work_bindings:app:app_id',
        'trigger_work_bindings:app:app_id',
        'queue_bindings:app:app_id',
        'managed_postgres_bindings:app:app_id',
        'object_buckets:app:app_id',
        'project_environment_workload_deployment_specs:deployment:deployment_id',
        'deployment_sidecar_layers:deployment:deployment_id',
        'deployment_sidecar_secret_reload_signals:deployment:deployment_id',
        'object_storage_s3_credentials:bucket:bucket_id',
        'object_bucket_versioning:bucket:bucket_id',
        'object_bucket_lifecycle:bucket:bucket_id',
        'object_bucket_encryption:bucket:bucket_id',
        'object_bucket_object_lock:bucket:bucket_id',
        'object_version_protection:bucket:bucket_id',
        'managed_postgres_databases:database:id'
    ] LOOP
        parts := string_to_array(entry, ':');
        EXECUTE format('DROP TRIGGER IF EXISTS clone_configuration_write_fence ON %I', parts[1]);
        EXECUTE format('CREATE TRIGGER clone_configuration_write_fence BEFORE INSERT OR UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION guard_clone_configuration_mutation(%L,%L)', parts[1], parts[2], parts[3]);
    END LOOP;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS clone_configuration_write_fence ON projects;
CREATE TRIGGER clone_configuration_write_fence BEFORE UPDATE OR DELETE ON projects
FOR EACH ROW EXECUTE FUNCTION guard_clone_configuration_mutation('project', 'id');

-- +goose Down
-- A downgrade cannot resume configuration writes behind a retained capture.
ALTER TABLE project_environment_clone_configuration_guards
    ADD CONSTRAINT clone_configuration_guards_down_no_ownership CHECK (state = 'open');
-- +goose StatementBegin
DO $$
DECLARE guarded_table text;
BEGIN
    FOR guarded_table IN SELECT c.relname FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND t.tgname = 'clone_configuration_write_fence'
    LOOP
        EXECUTE format('DROP TRIGGER clone_configuration_write_fence ON %I', guarded_table);
    END LOOP;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER initialize_clone_configuration_guard ON projects;
DROP FUNCTION guard_clone_configuration_mutation();
DROP FUNCTION assert_clone_configuration_mutable(uuid);
DROP FUNCTION initialize_clone_configuration_guard();
DROP TABLE project_environment_clone_configuration_clock;
DROP TABLE project_environment_clone_configuration_guards;
