-- +goose Up
-- +goose StatementBegin
-- Provider-independent application access to logical object buckets.
CREATE TABLE IF NOT EXISTS object_storage_access_grants (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE CASCADE,
    api_key_id uuid NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    permission text NOT NULL CHECK (permission IN ('read', 'write', 'read_write')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_id, api_key_id)
);
CREATE INDEX IF NOT EXISTS object_storage_access_grants_key_idx
    ON object_storage_access_grants (account_id, api_key_id, bucket_id);

-- Rotation must not silently strand an application. Copying the logical
-- grants is safe because the successor inherits the predecessor's scopes.
CREATE OR REPLACE FUNCTION copy_object_storage_grants_on_key_rotation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.rotated_from_id IS NOT NULL THEN
        INSERT INTO object_storage_access_grants
            (account_id, bucket_id, api_key_id, permission, created_at, updated_at)
        SELECT account_id, bucket_id, NEW.id, permission, now(), now()
          FROM object_storage_access_grants
         WHERE api_key_id = NEW.rotated_from_id
        ON CONFLICT (bucket_id, api_key_id) DO UPDATE
            SET permission = EXCLUDED.permission, updated_at = now();
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS api_key_rotation_copy_object_storage_grants ON api_keys;
CREATE TRIGGER api_key_rotation_copy_object_storage_grants
AFTER INSERT ON api_keys
FOR EACH ROW WHEN (NEW.rotated_from_id IS NOT NULL)
EXECUTE FUNCTION copy_object_storage_grants_on_key_rotation();

-- object_buckets are soft-deleted, so their FK never fires during normal
-- deletion. Remove grants when the lifecycle reaches its tombstone state.
CREATE OR REPLACE FUNCTION delete_object_storage_grants_on_bucket_delete()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM object_storage_access_grants WHERE bucket_id = NEW.id;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS object_bucket_delete_access_grants ON object_buckets;
CREATE TRIGGER object_bucket_delete_access_grants
AFTER UPDATE OF state ON object_buckets
FOR EACH ROW WHEN (NEW.state = 'deleted' AND OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION delete_object_storage_grants_on_bucket_delete();

-- Keep the database vocabulary in lockstep with pkg/api/apikey.go. This also
-- reconciles registry/upstream scopes that older schema snapshots omitted.
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'apps:read', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'env:read', 'env:write', 'registry_credentials:read',
        'registry_credentials:write', 'upstreams:write', 'storage:manage',
        'storage:read', 'storage:write'
    ]::text[] AND cardinality(scopes) > 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scopes_vocab_chk;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scopes_vocab_chk CHECK (
    scopes <@ ARRAY[
        'admin', 'deploy:write', 'secrets:read', 'secrets:write',
        'usage:read', 'apps:read', 'env:read', 'env:write'
    ]::text[] AND cardinality(scopes) > 0
);
DROP TRIGGER IF EXISTS object_bucket_delete_access_grants ON object_buckets;
DROP FUNCTION IF EXISTS delete_object_storage_grants_on_bucket_delete();
DROP TRIGGER IF EXISTS api_key_rotation_copy_object_storage_grants ON api_keys;
DROP FUNCTION IF EXISTS copy_object_storage_grants_on_key_rotation();
DROP TABLE IF EXISTS object_storage_access_grants;
-- +goose StatementEnd
