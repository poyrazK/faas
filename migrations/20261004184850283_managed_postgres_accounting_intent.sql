-- filename: 20261004184850283_managed_postgres_accounting_intent.sql
-- ADR-581: accounting ownership survives uncertain provisioning and deletion.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_databases
    ADD COLUMN IF NOT EXISTS accounting_required boolean NOT NULL DEFAULT true;
-- The true default also covers inserts from older binaries during upgrade.
-- The new reservation query explicitly records an unattempted false row.
-- Historical attempt counters were reset by readiness and deletion. There is
-- no reliable evidence that an existing unknown row never reached a provider.
-- The constant default backfills these rows without a table-wide UPDATE.

CREATE OR REPLACE FUNCTION guard_managed_postgres_accounting_intent() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.accounting_required AND NOT NEW.accounting_required THEN
        RAISE EXCEPTION 'managed postgres accounting obligation cannot be cleared'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_accounting_intent_retained';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS guard_managed_postgres_accounting_intent ON managed_postgres_databases;
CREATE TRIGGER guard_managed_postgres_accounting_intent
    BEFORE UPDATE OF accounting_required ON managed_postgres_databases
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_accounting_intent();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM managed_postgres_databases
               WHERE accounting_required AND NULLIF(provider_resource_id, '') IS NULL) THEN
        RAISE EXCEPTION 'cannot downgrade unresolved managed postgres accounting'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_accounting_downgrade_unresolved';
    END IF;
END;
$$;
DROP TRIGGER IF EXISTS guard_managed_postgres_accounting_intent ON managed_postgres_databases;
DROP FUNCTION IF EXISTS guard_managed_postgres_accounting_intent();
ALTER TABLE managed_postgres_databases DROP COLUMN accounting_required;
-- +goose StatementEnd
