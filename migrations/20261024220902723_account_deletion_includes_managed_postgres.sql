-- filename: 20261024220902723_account_deletion_includes_managed_postgres.sql
-- migration-compat: expand
--
-- ADR-20261024150500: an account deletion can include the account's personal
-- managed PostgreSQL databases. The flag is set in the same UPDATE that moves
-- the account to deleted_pending; the guard lets that transition through and
-- clears the flag on every other status, so a restored or paid-back account
-- forgets the choice. Older writers never set it and keep today's guard.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS deletion_includes_managed_postgres boolean NOT NULL DEFAULT false;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE live bigint;
BEGIN
    IF NEW.status <> 'deleted_pending' THEN
        NEW.deletion_includes_managed_postgres := false;
        RETURN NEW;
    END IF;
    IF NEW.deletion_includes_managed_postgres THEN
        RETURN NEW;
    END IF;
    SELECT count(*) INTO live FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
    WHERE d.account_id=NEW.id AND d.state<>'deleted'
      AND (o.personal_org OR NOT o.resource_ownership_enabled);
    IF live > 0 THEN
        RAISE EXCEPTION 'account has managed postgres databases'
            USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases', DETAIL = live::text;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- The flag alone must not change outside a status transition either.
DROP TRIGGER IF EXISTS account_managed_postgres_databases_guard ON accounts;
CREATE TRIGGER account_managed_postgres_databases_guard
    BEFORE UPDATE OF status, deletion_includes_managed_postgres ON accounts
    FOR EACH ROW EXECUTE FUNCTION guard_account_managed_postgres_databases();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS account_managed_postgres_databases_guard ON accounts;
CREATE TRIGGER account_managed_postgres_databases_guard
    BEFORE UPDATE OF status ON accounts
    FOR EACH ROW EXECUTE FUNCTION guard_account_managed_postgres_databases();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE live bigint;
BEGIN
    IF NEW.status = 'deleted_pending' THEN
        SELECT count(*) INTO live FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
        WHERE d.account_id=NEW.id AND d.state<>'deleted'
          AND (o.personal_org OR NOT o.resource_ownership_enabled);
        IF live > 0 THEN
            RAISE EXCEPTION 'account has managed postgres databases'
                USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases', DETAIL = live::text;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER TABLE accounts DROP COLUMN IF EXISTS deletion_includes_managed_postgres;
