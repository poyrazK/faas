-- filename: 20261102104443407_object_storage_ceiling_grants.sql
-- migration-compat: expand
-- ADR-20261101214152: a paid account's object-storage ceiling is its plan's
-- (Hobby 20 GiB, Pro 250 GiB, Scale 1,000 GiB) unless support grants it
-- another. One row per billing subject, an account or a resource-owning org,
-- so the table has no foreign key; the spend cap still tightens a granted
-- ceiling, and a Free subject ignores its row. apid is the only writer
-- (POST /v1/admin/ops/accounts/{id}/object-storage-ceiling). A previous
-- release neither writes nor reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS object_storage_ceiling_grants (
    owner_id uuid PRIMARY KEY,
    max_bytes bigint NOT NULL CHECK (max_bytes > 0 AND max_bytes <= 1152921504606846976),
    reason text NOT NULL CHECK (reason ~ '^[a-z0-9_]{1,64}$'),
    granted_by uuid NOT NULL,
    granted_at timestamptz NOT NULL CHECK (isfinite(granted_at))
);
COMMENT ON TABLE object_storage_ceiling_grants IS 'Object-storage ceilings support granted a billing subject in place of its paid plan''s; written only by apid (ADR-20261101214152).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS object_storage_ceiling_grants;
-- +goose StatementEnd
