-- filename: 20261021081233450_project_environment_secrets.sql

-- ADR-1139: secrets shared by every service of a project environment. One row
-- per (project, environment, key) holds a single-key age envelope sealed to the
-- fleet recipient. A value change stamps a runtime-configuration change for
-- every member app in the environment's scope and marks those snapshots stale,
-- as ADR-1136's configuration trigger does; a master-key reseal does not. The
-- reseal worker and key retirement cover the rows as kind 'project'.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS project_environment_secrets (
    account_id uuid NOT NULL REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment_slug text NOT NULL,
    key text NOT NULL,
    ciphertext bytea NOT NULL,
    kid text NOT NULL,
    value_hash text NOT NULL,
    secret_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, environment_slug, key),
    CONSTRAINT project_environment_secrets_environment_slug_shape CHECK (environment_slug ~ '^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$'),
    CONSTRAINT project_environment_secrets_key_shape CHECK (key ~ '^[A-Z][A-Z0-9_]*$' AND length(key) <= 128),
    CONSTRAINT project_environment_secrets_ciphertext_present CHECK (octet_length(ciphertext) > 0),
    CONSTRAINT project_environment_secrets_kid_shape CHECK (kid ~ '^age1[a-z0-9]+$' AND length(kid) <= 128),
    CONSTRAINT project_environment_secrets_value_hash_shape CHECK (length(value_hash) BETWEEN 1 AND 16),
    CONSTRAINT project_environment_secrets_secret_version_positive CHECK (secret_version >= 1)
);

CREATE INDEX IF NOT EXISTS project_environment_secrets_account_idx ON project_environment_secrets (account_id);

-- +goose StatementBegin
-- The same lock, fence and retired-key check as guard_release_secret_key_retirement,
-- plus: identity columns never change, and a value change outside a reseal
-- advances the version so the runtime-change trigger sees it.
CREATE OR REPLACE FUNCTION guard_project_environment_secret_write()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(hashtextextended('release-secret-key-retirement', 0));
    PERFORM generation FROM release_secret_key_retirement_fence WHERE singleton FOR SHARE;
    IF EXISTS (SELECT 1 FROM release_secret_key_retirements r WHERE r.kid = NEW.kid) THEN
        RAISE EXCEPTION 'encrypted release input uses a retired or unlabelled key'
            USING ERRCODE = '23514', CONSTRAINT = 'release_secret_key_retired';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        IF (NEW.account_id, NEW.project_id, NEW.environment_slug, NEW.key)
           IS DISTINCT FROM (OLD.account_id, OLD.project_id, OLD.environment_slug, OLD.key) THEN
            RAISE EXCEPTION 'project environment secret identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'project_environment_secrets_identity_immutable';
        END IF;
        IF NEW.secret_version = OLD.secret_version
           AND (NEW.value_hash IS DISTINCT FROM OLD.value_hash
                OR (NEW.ciphertext IS DISTINCT FROM OLD.ciphertext
                    AND current_setting('faas.release_secret_rekey', true) IS DISTINCT FROM 'on')) THEN
            RAISE EXCEPTION 'a project environment secret value change must advance its version'
                USING ERRCODE = '23514', CONSTRAINT = 'project_environment_secrets_version_advances';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER project_environment_secret_write_guard
    BEFORE INSERT OR UPDATE ON project_environment_secrets
    FOR EACH ROW EXECUTE FUNCTION guard_project_environment_secret_write();

-- +goose StatementBegin
-- Mirrors project_environment_config_runtime_change (ADR-1136): every member
-- app's scope is stamped and its snapshots in that scope go stale; production
-- also stamps the default scope's own project boundary.
CREATE OR REPLACE FUNCTION project_environment_secret_runtime_change()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    r record;
    changed timestamptz := clock_timestamp();
BEGIN
    IF TG_OP = 'DELETE' THEN
        r := OLD;
    ELSE
        r := NEW;
    END IF;
    WITH member AS (
        SELECT a.id FROM apps a
         WHERE a.project_id = r.project_id AND a.account_id = r.account_id AND a.status <> 'deleted'
    ), stamped AS (
        INSERT INTO app_runtime_config_scope_changes (app_id, scope, changed_at)
        SELECT member.id, r.environment_slug, changed FROM member
        ON CONFLICT (app_id, scope) DO UPDATE
            SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at)
        RETURNING app_id, scope, changed_at
    )
    UPDATE snapshots SET stale = true
      FROM deployments d, stamped
     WHERE snapshots.deployment_id = d.id AND d.app_id = stamped.app_id
       AND coalesce(nullif(d.scope, ''), 'default') = stamped.scope
       AND NOT snapshots.stale AND snapshots.created_at <= stamped.changed_at;
    IF r.environment_slug = 'production' THEN
        INSERT INTO app_runtime_config_scope_changes (app_id, scope, changed_at)
        SELECT a.id, 'default:project-config', changed FROM apps a
         WHERE a.project_id = r.project_id AND a.account_id = r.account_id AND a.status <> 'deleted'
        ON CONFLICT (app_id, scope) DO UPDATE
            SET changed_at = greatest(app_runtime_config_scope_changes.changed_at, excluded.changed_at);
        UPDATE snapshots SET stale = true
          FROM deployments d, apps a
         WHERE snapshots.deployment_id = d.id AND d.app_id = a.id
           AND a.project_id = r.project_id AND a.account_id = r.account_id AND a.status <> 'deleted'
           AND coalesce(nullif(d.scope, ''), 'default') = 'default'
           AND NOT snapshots.stale AND snapshots.created_at <= changed;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER project_environment_secret_runtime_change
    AFTER INSERT OR DELETE ON project_environment_secrets
    FOR EACH ROW EXECUTE FUNCTION project_environment_secret_runtime_change();

-- A reseal changes only kid and ciphertext, which does not change what a
-- guest receives.
CREATE OR REPLACE TRIGGER project_environment_secret_value_change
    AFTER UPDATE ON project_environment_secrets
    FOR EACH ROW
    WHEN (OLD.secret_version IS DISTINCT FROM NEW.secret_version OR OLD.value_hash IS DISTINCT FROM NEW.value_hash)
    EXECUTE FUNCTION project_environment_secret_runtime_change();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION release_secret_project_document(s project_environment_secrets)
RETURNS jsonb LANGUAGE sql STABLE AS $$
    SELECT jsonb_build_array(jsonb_build_object('key', (s).key, 'kid', (s).kid,
        'ciphertext', encode((s).ciphertext, 'base64'), 'value_hash', (s).value_hash,
        'secret_version', (s).secret_version, 'created_at', (s).created_at, 'updated_at', (s).updated_at))
$$;
-- +goose StatementEnd

CREATE OR REPLACE VIEW release_secret_rekey_documents AS
 SELECT 'app'::text AS kind,
    (((((((s.account_id)::text || '/'::text) || (s.app_id)::text) || '/'::text) || s.scope) || '/'::text) || s.key) AS id,
    release_secret_app_document(s.*) AS payload
   FROM app_secrets s
UNION ALL
 SELECT 'bundle'::text AS kind,
    (((project_release_secret_bundles.account_id)::text || '/'::text) || project_release_secret_bundles.request_id) AS id,
    project_release_secret_bundles.values_json AS payload
   FROM project_release_secret_bundles
UNION ALL
 SELECT 'capture'::text AS kind,
    (((project_release_config_intents.account_id)::text || '/'::text) || project_release_config_intents.attempt_id) AS id,
    project_release_config_intents.payload
   FROM project_release_config_intents
UNION ALL
 SELECT 'deployment'::text AS kind,
    (deployment_release_secret_snapshots.deployment_id)::text AS id,
    deployment_release_secret_snapshots.values_json AS payload
   FROM deployment_release_secret_snapshots
UNION ALL
 SELECT 'promotion'::text AS kind,
    (project_environment_promotion_release_config.promotion_id)::text AS id,
    project_environment_promotion_release_config.payload
   FROM project_environment_promotion_release_config
  WHERE (project_environment_promotion_release_config.payload ? 'secrets'::text)
UNION ALL
 SELECT 'project'::text AS kind,
    (((((((p.account_id)::text || '/'::text) || (p.project_id)::text) || '/'::text) || p.environment_slug) || '/'::text) || p.key) AS id,
    release_secret_project_document(p.*) AS payload
   FROM project_environment_secrets p;

-- +goose Down
CREATE OR REPLACE VIEW release_secret_rekey_documents AS
 SELECT 'app'::text AS kind,
    (((((((s.account_id)::text || '/'::text) || (s.app_id)::text) || '/'::text) || s.scope) || '/'::text) || s.key) AS id,
    release_secret_app_document(s.*) AS payload
   FROM app_secrets s
UNION ALL
 SELECT 'bundle'::text AS kind,
    (((project_release_secret_bundles.account_id)::text || '/'::text) || project_release_secret_bundles.request_id) AS id,
    project_release_secret_bundles.values_json AS payload
   FROM project_release_secret_bundles
UNION ALL
 SELECT 'capture'::text AS kind,
    (((project_release_config_intents.account_id)::text || '/'::text) || project_release_config_intents.attempt_id) AS id,
    project_release_config_intents.payload
   FROM project_release_config_intents
UNION ALL
 SELECT 'deployment'::text AS kind,
    (deployment_release_secret_snapshots.deployment_id)::text AS id,
    deployment_release_secret_snapshots.values_json AS payload
   FROM deployment_release_secret_snapshots
UNION ALL
 SELECT 'promotion'::text AS kind,
    (project_environment_promotion_release_config.promotion_id)::text AS id,
    project_environment_promotion_release_config.payload
   FROM project_environment_promotion_release_config
  WHERE (project_environment_promotion_release_config.payload ? 'secrets'::text);
DROP FUNCTION IF EXISTS release_secret_project_document(project_environment_secrets);
DROP TABLE IF EXISTS project_environment_secrets;
DROP FUNCTION IF EXISTS project_environment_secret_runtime_change();
DROP FUNCTION IF EXISTS guard_project_environment_secret_write();
