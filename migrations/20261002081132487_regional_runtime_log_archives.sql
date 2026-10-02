-- filename: 20261002081132487_regional_runtime_log_archives.sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS runtime_log_archive_bindings (
    instance_id text PRIMARY KEY CHECK (instance_id ~ '^[A-Za-z0-9._-]{1,128}$' AND instance_id NOT IN ('.', '..')),
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    deployment_id uuid NOT NULL, -- Historical ownership survives deployment garbage collection.
    compute_region text NOT NULL DEFAULT '' CHECK (compute_region = '' OR (length(compute_region) <= 63 AND compute_region ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS runtime_log_archive_bindings_app ON runtime_log_archive_bindings(app_id);
CREATE TABLE IF NOT EXISTS runtime_log_archives (
    instance_id text NOT NULL REFERENCES runtime_log_archive_bindings(instance_id) ON DELETE CASCADE,
    archive_day date NOT NULL,
    storage_key text NOT NULL UNIQUE CHECK (storage_key <> ''),
    bytes bigint NOT NULL DEFAULT 0,
    sha256 text NOT NULL DEFAULT '',
    published_at timestamptz,
    PRIMARY KEY (instance_id, archive_day),
    CONSTRAINT runtime_log_archives_publication CHECK (
        (bytes = 0 AND sha256 = '' AND published_at IS NULL)
        OR (bytes > 0 AND sha256 ~ '^[0-9a-f]{64}$' AND published_at IS NOT NULL)
    )
);
CREATE TABLE IF NOT EXISTS runtime_log_archive_fragments (
    instance_id text NOT NULL,
    archive_day date NOT NULL,
    fragment_sha256 text NOT NULL CHECK (fragment_sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (instance_id, archive_day, fragment_sha256),
    FOREIGN KEY (instance_id, archive_day) REFERENCES runtime_log_archives(instance_id, archive_day) ON DELETE CASCADE
);
CREATE OR REPLACE FUNCTION enforce_runtime_log_archive_binding() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF (NEW.instance_id, NEW.app_id, NEW.account_id, NEW.deployment_id, NEW.compute_region)
           IS DISTINCT FROM (OLD.instance_id, OLD.app_id, OLD.account_id, OLD.deployment_id, OLD.compute_region) THEN
            RAISE EXCEPTION 'runtime log archive identity is immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_binding_immutable';
        END IF;
    ELSIF NOT EXISTS (
        SELECT 1 FROM instances i JOIN deployments d ON d.id = i.deployment_id JOIN apps a ON a.id = d.app_id
         WHERE i.id::text = NEW.instance_id AND i.app_id = d.app_id AND a.status = 'active'
           AND EXISTS (SELECT 1 FROM accounts acct WHERE acct.id = a.account_id AND acct.status = 'active')
           AND (d.app_id, a.account_id, d.id, a.compute_region)
               = (NEW.app_id, NEW.account_id, NEW.deployment_id, NEW.compute_region)
    ) THEN
        RAISE EXCEPTION 'runtime log archive binding requires its durable instance owner'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_binding_owner';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS runtime_log_archive_binding_guard ON runtime_log_archive_bindings;
CREATE TRIGGER runtime_log_archive_binding_guard BEFORE INSERT OR UPDATE ON runtime_log_archive_bindings
    FOR EACH ROW EXECUTE FUNCTION enforce_runtime_log_archive_binding();
CREATE OR REPLACE FUNCTION enforce_runtime_log_archive_key() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE selected_region text; expected_key text;
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.instance_id, NEW.archive_day, NEW.storage_key)
        IS DISTINCT FROM (OLD.instance_id, OLD.archive_day, OLD.storage_key) THEN
        RAISE EXCEPTION 'runtime log archive key is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_key_immutable';
    END IF;
    SELECT compute_region INTO selected_region FROM runtime_log_archive_bindings WHERE instance_id = NEW.instance_id;
    expected_key := CASE WHEN selected_region = '' THEN '' ELSE 'regions/' || selected_region || '/' END
       || 'faas-logs/' || NEW.instance_id || '/' || to_char(NEW.archive_day, 'YYYY/MM/YYYY-MM-DD') || '.jsonl.gz';
    IF NEW.storage_key IS DISTINCT FROM expected_key THEN
        RAISE EXCEPTION 'runtime log archive key must match its binding and day'
            USING ERRCODE = '23514', CONSTRAINT = 'runtime_log_archive_key_identity';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS runtime_log_archive_key_guard ON runtime_log_archives;
CREATE TRIGGER runtime_log_archive_key_guard BEFORE INSERT OR UPDATE ON runtime_log_archives
    FOR EACH ROW EXECUTE FUNCTION enforce_runtime_log_archive_key();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $guard$ BEGIN
    IF EXISTS (SELECT 1 FROM runtime_log_archive_bindings) THEN
        RAISE EXCEPTION 'cannot discard retained runtime log archive ownership';
    END IF;
END $guard$;
DROP TRIGGER runtime_log_archive_key_guard ON runtime_log_archives;
DROP FUNCTION enforce_runtime_log_archive_key();
DROP TRIGGER runtime_log_archive_binding_guard ON runtime_log_archive_bindings;
DROP FUNCTION enforce_runtime_log_archive_binding();
DROP TABLE runtime_log_archive_fragments;
DROP TABLE runtime_log_archives;
DROP TABLE runtime_log_archive_bindings;
-- +goose StatementEnd
