-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS owner_kind text NOT NULL DEFAULT 'operator';

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_owner_kind_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_owner_kind_chk
                CHECK (owner_kind IN ('operator', 'customer'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_customer_integration_policy_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_customer_integration_policy_chk CHECK (
                owner_kind <> 'customer'
                OR (provider_auth_mode = 'managed'
                    AND credential_source = 'customer_sealed'
                    AND cardinality(allowed_methods) BETWEEN 1 AND 6
                    AND cardinality(allowed_path_prefixes) BETWEEN 1 AND 32)
            );
    END IF;
END$$;
-- +goose StatementEnd

-- The owner boundary is immutable. Operator reconciliation may update only
-- operator rows; customer API code may only create or delete customer rows.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_outbound_integration_owner_kind() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.owner_kind <> NEW.owner_kind THEN
        RAISE EXCEPTION 'outbound integration owner kind is immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'outbound_integration_owner_kind_immutable';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS outbound_integration_owner_kind_guard ON outbound_integrations;
CREATE TRIGGER outbound_integration_owner_kind_guard
    BEFORE UPDATE OF owner_kind ON outbound_integrations
    FOR EACH ROW EXECUTE FUNCTION guard_outbound_integration_owner_kind();

-- +goose Down
DROP TRIGGER outbound_integration_owner_kind_guard ON outbound_integrations;
DROP FUNCTION guard_outbound_integration_owner_kind();
ALTER TABLE outbound_integrations
    DROP CONSTRAINT outbound_customer_integration_policy_chk,
    DROP CONSTRAINT outbound_integrations_owner_kind_chk,
    DROP COLUMN owner_kind;
