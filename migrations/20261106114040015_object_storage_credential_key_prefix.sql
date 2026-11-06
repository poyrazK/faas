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

ALTER TABLE object_storage_s3_credentials ADD COLUMN IF NOT EXISTS key_prefix text;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE object_storage_s3_credentials DROP COLUMN IF EXISTS key_prefix;

RESET lock_timeout;
