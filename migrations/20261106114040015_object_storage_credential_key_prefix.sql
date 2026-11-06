-- filename: 20261106114040015_object_storage_credential_key_prefix.sql
-- migration-compat: expand

-- ADR-20261106114500: a compute binding may be limited to object keys under
-- one prefix. The binding's key and the rotation stage that keeps its
-- previous key both record the prefix; the S3 gateway refuses every request
-- outside it. Customer-created keys never carry one.
--
-- An older gateway ignores the column, so apid issues scoped keys only for
-- accounts the object-storage-key-scopes capability is released to, after
-- the gateway that enforces it is deployed.
--
-- The column is nullable with no default, so adding it writes no rows.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- The CHECK mirrors api.ValidObjectKeyScope's shape (2 to 512 bytes, ending
-- in "/", no leading "/") and allows the prefix only on a compute binding or
-- the rotation stage of one, never on a signed-URL credential. It is added
-- NOT VALID, so new writes are checked at once without scanning the table;
-- every existing row is NULL, and a later migration validates it.
ALTER TABLE object_storage_s3_credentials ADD COLUMN IF NOT EXISTS key_prefix text;

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'object_storage_s3_credentials_key_prefix_check'
                AND conrelid = 'object_storage_s3_credentials'::regclass) THEN
  ALTER TABLE object_storage_s3_credentials ADD CONSTRAINT object_storage_s3_credentials_key_prefix_check CHECK (key_prefix IS NULL OR (
      length(key_prefix) BETWEEN 2 AND 512 AND right(key_prefix, 1) = '/' AND left(key_prefix, 1) <> '/'
      AND url_request IS NULL AND (managed_app_id IS NOT NULL OR rotation_parent_id IS NOT NULL))) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE object_storage_s3_credentials DROP CONSTRAINT IF EXISTS object_storage_s3_credentials_key_prefix_check;
ALTER TABLE object_storage_s3_credentials DROP COLUMN IF EXISTS key_prefix;

RESET lock_timeout;
