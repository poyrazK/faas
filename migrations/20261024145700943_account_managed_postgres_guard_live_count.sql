-- filename: 20261024145700943_account_managed_postgres_guard_live_count.sql
-- migration-compat: expand
--
-- The account deletion guard now reports how many live managed PostgreSQL
-- databases block the move to deleted_pending, in the error DETAIL. apid
-- turns it into a 409 that names the count and the next action, and meterd
-- reports suspended accounts the nonpayment deletion cannot advance. The
-- predicate is unchanged; older readers ignore the DETAIL.

-- +goose Up
SET LOCAL lock_timeout = '5s';
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

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.status = 'deleted_pending' AND EXISTS (
        SELECT 1 FROM managed_postgres_databases d JOIN orgs o ON o.id=d.org_id
        WHERE d.account_id=NEW.id AND d.state<>'deleted'
          AND (o.personal_org OR NOT o.resource_ownership_enabled)
    ) THEN
        RAISE EXCEPTION 'account has managed postgres databases'
            USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
