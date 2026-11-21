-- filename: 20261119102834810_managed_postgres_custom_role_default_for.sql
-- migration-compat: expand
-- Default custom roles (ADR-20261116213000, amendment 2). default_for lists
-- the access modes whose logins start every session as the role. A new
-- column with a constant default; older apid neither reads nor writes it, and
-- its rows keep the empty default.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_custom_roles
    ADD COLUMN IF NOT EXISTS default_for text[] NOT NULL DEFAULT '{}';

-- NOT VALID: existing rows hold the empty default, which satisfies it; new
-- and updated rows are checked. A later migration may VALIDATE it.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'managed_postgres_custom_roles_default_for_check'
                     AND conrelid = 'managed_postgres_custom_roles'::regclass) THEN
        ALTER TABLE managed_postgres_custom_roles
            ADD CONSTRAINT managed_postgres_custom_roles_default_for_check
                CHECK (default_for <@ ARRAY['read_only', 'read_write']::text[] AND default_for <@ granted_to) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE managed_postgres_custom_roles DROP COLUMN IF EXISTS default_for;
