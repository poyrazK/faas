-- +goose Up
CREATE TABLE IF NOT EXISTS outbound_app_bindings (
    account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id          uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    integration_id  uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (app_id, integration_id)
);
CREATE INDEX IF NOT EXISTS outbound_app_bindings_integration_idx
    ON outbound_app_bindings (integration_id, app_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_outbound_app_binding_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM apps app
          JOIN outbound_integrations integration
            ON integration.id = NEW.integration_id
         WHERE app.id = NEW.app_id
           AND app.account_id = NEW.account_id
           AND app.status <> 'deleted'
           AND integration.account_id = NEW.account_id
           AND integration.provider_auth_mode = 'managed'
    ) THEN
        RAISE EXCEPTION 'outbound binding owner mismatch or integration is not managed'
            USING ERRCODE = '23514', CONSTRAINT = 'outbound_app_binding_owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS outbound_app_binding_owner_guard ON outbound_app_bindings;
CREATE TRIGGER outbound_app_binding_owner_guard
    BEFORE INSERT OR UPDATE OF account_id,app_id,integration_id ON outbound_app_bindings
    FOR EACH ROW EXECUTE FUNCTION guard_outbound_app_binding_owner();

-- +goose Down
DROP TRIGGER outbound_app_binding_owner_guard ON outbound_app_bindings;
DROP FUNCTION guard_outbound_app_binding_owner();
DROP INDEX outbound_app_bindings_integration_idx;
DROP TABLE outbound_app_bindings;
