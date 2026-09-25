-- +goose Up
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS credential_source text NOT NULL DEFAULT 'operator_env';

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_credential_source_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_credential_source_chk
                CHECK (credential_source IN ('operator_env', 'customer_sealed'));
    END IF;
END$$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS outbound_integration_credentials (
    integration_id uuid PRIMARY KEY REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    authorization_sealed bytea NOT NULL
        CHECK (octet_length(authorization_sealed) BETWEEN 1 AND 65536),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_outbound_credential_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM outbound_integrations integration
         WHERE integration.id = NEW.integration_id
           AND integration.account_id = NEW.account_id
           AND integration.provider_auth_mode = 'managed'
           AND integration.credential_source = 'customer_sealed'
    ) THEN
        RAISE EXCEPTION 'outbound credential owner or source mismatch'
            USING ERRCODE = '23514', CONSTRAINT = 'outbound_credential_owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS outbound_credential_owner_guard ON outbound_integration_credentials;
CREATE TRIGGER outbound_credential_owner_guard
    BEFORE INSERT OR UPDATE OF integration_id,account_id,authorization_sealed ON outbound_integration_credentials
    FOR EACH ROW EXECUTE FUNCTION guard_outbound_credential_owner();

-- Do not reactivate an old customer key if an operator changes source away
-- from customer_sealed and later changes it back.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION purge_inactive_outbound_credential() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.credential_source = 'customer_sealed'
       AND (NEW.credential_source <> 'customer_sealed' OR NEW.provider_auth_mode <> 'managed') THEN
        DELETE FROM outbound_integration_credentials WHERE integration_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS outbound_credential_source_guard ON outbound_integrations;
CREATE TRIGGER outbound_credential_source_guard
    AFTER UPDATE OF credential_source,provider_auth_mode ON outbound_integrations
    FOR EACH ROW EXECUTE FUNCTION purge_inactive_outbound_credential();

-- +goose Down
DROP TRIGGER outbound_credential_source_guard ON outbound_integrations;
DROP FUNCTION purge_inactive_outbound_credential();
DROP TRIGGER outbound_credential_owner_guard ON outbound_integration_credentials;
DROP FUNCTION guard_outbound_credential_owner();
DROP TABLE outbound_integration_credentials;
ALTER TABLE outbound_integrations DROP COLUMN credential_source;
