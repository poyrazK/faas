-- ADR-1193: an inbound webhook endpoint can start customer Operations.
-- +goose Up
CREATE SEQUENCE IF NOT EXISTS customer_operation_webhook_binding_revision_seq;

-- The owner is fixed by the authenticated binding, never by provider payload.
CREATE TABLE IF NOT EXISTS customer_operation_webhook_bindings (
    endpoint_id uuid PRIMARY KEY REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 64),
    operation_name text NOT NULL CHECK (operation_name ~ '^[a-z][a-z0-9-]{0,63}$'),
    platform_tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    event_types text[] NOT NULL DEFAULT '{}' CHECK (cardinality(event_types) <= 32 AND array_position(event_types, NULL) IS NULL),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS customer_operation_webhook_bindings_tenant_idx
    ON customer_operation_webhook_bindings (account_id, platform_tenant_id);

-- One provider event admits at most one operation, independently of later
-- rebinding. Operations are referenced by ID only: their retention must never
-- delete the record that prevents a second execution.
CREATE TABLE IF NOT EXISTS customer_operation_webhook_receipts (
    endpoint_id uuid NOT NULL REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    provider_event_id text NOT NULL CHECK (octet_length(provider_event_id) BETWEEN 1 AND 256),
    body_sha256 bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    account_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    binding_version bigint NOT NULL CHECK (binding_version > 0),
    accepted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (endpoint_id, provider_event_id),
    CHECK (expires_at > accepted_at)
);
CREATE INDEX IF NOT EXISTS customer_operation_webhook_receipts_expiry_idx
    ON customer_operation_webhook_receipts (endpoint_id, expires_at);

-- One endpoint has one delivery mode. Extend the ADR-212 routing guard so an
-- operation binding excludes automations and exclusive operation bindings,
-- including writes from an older API binary. Callback bindings stay compatible.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_workflow_webhook_routing() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE endpoint uuid;
BEGIN
    IF TG_TABLE_NAME = 'exclusive_work_trigger_bindings' THEN
        IF NEW.source <> 'inbound_webhook' THEN RETURN NEW; END IF;
        endpoint := NEW.trigger_id;
    ELSE
        endpoint := NEW.endpoint_id;
    END IF;
    PERFORM 1 FROM inbound_webhook_endpoints WHERE id = endpoint FOR UPDATE;
    IF TG_TABLE_NAME = 'workflow_webhook_bindings' THEN
        IF EXISTS (SELECT 1 FROM workflow_callback_webhook_bindings WHERE endpoint_id = endpoint)
           OR EXISTS (SELECT 1 FROM exclusive_work_trigger_bindings WHERE source = 'inbound_webhook' AND trigger_id = endpoint)
           OR EXISTS (SELECT 1 FROM customer_operation_webhook_bindings WHERE endpoint_id = endpoint) THEN
            RAISE EXCEPTION 'endpoint already has a callback or operation binding' USING ERRCODE = '23505';
        END IF;
    ELSIF EXISTS (SELECT 1 FROM workflow_webhook_bindings WHERE endpoint_id = endpoint) THEN
        RAISE EXCEPTION 'endpoint already starts an automation' USING ERRCODE = '23505';
    ELSIF TG_TABLE_NAME = 'exclusive_work_trigger_bindings'
          AND EXISTS (SELECT 1 FROM customer_operation_webhook_bindings WHERE endpoint_id = endpoint) THEN
        RAISE EXCEPTION 'endpoint already starts a customer operation' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;
-- The binding table has its own guard function so the ADR-212 migration can
-- still roll back and drop the shared one.
CREATE OR REPLACE FUNCTION guard_customer_operation_webhook_routing() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM inbound_webhook_endpoints WHERE id = NEW.endpoint_id FOR UPDATE;
    IF EXISTS (SELECT 1 FROM workflow_webhook_bindings WHERE endpoint_id = NEW.endpoint_id)
       OR EXISTS (SELECT 1 FROM exclusive_work_trigger_bindings WHERE source = 'inbound_webhook' AND trigger_id = NEW.endpoint_id) THEN
        RAISE EXCEPTION 'endpoint already has another delivery mode' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS customer_operation_webhook_routing_guard ON customer_operation_webhook_bindings;
CREATE TRIGGER customer_operation_webhook_routing_guard BEFORE INSERT OR UPDATE ON customer_operation_webhook_bindings
FOR EACH ROW EXECUTE FUNCTION guard_customer_operation_webhook_routing();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS customer_operation_webhook_routing_guard ON customer_operation_webhook_bindings;
DROP FUNCTION IF EXISTS guard_customer_operation_webhook_routing();
CREATE OR REPLACE FUNCTION guard_workflow_webhook_routing() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE endpoint uuid;
BEGIN
    IF TG_TABLE_NAME = 'exclusive_work_trigger_bindings' THEN
        IF NEW.source <> 'inbound_webhook' THEN RETURN NEW; END IF;
        endpoint := NEW.trigger_id;
    ELSE
        endpoint := NEW.endpoint_id;
    END IF;
    PERFORM 1 FROM inbound_webhook_endpoints WHERE id = endpoint FOR UPDATE;
    IF TG_TABLE_NAME = 'workflow_webhook_bindings' THEN
        IF EXISTS (SELECT 1 FROM workflow_callback_webhook_bindings WHERE endpoint_id = endpoint)
           OR EXISTS (SELECT 1 FROM exclusive_work_trigger_bindings WHERE source = 'inbound_webhook' AND trigger_id = endpoint) THEN
            RAISE EXCEPTION 'endpoint already has a callback or operation binding' USING ERRCODE = '23505';
        END IF;
    ELSIF EXISTS (SELECT 1 FROM workflow_webhook_bindings WHERE endpoint_id = endpoint) THEN
        RAISE EXCEPTION 'endpoint already starts an automation' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TABLE IF EXISTS customer_operation_webhook_receipts;
DROP TABLE IF EXISTS customer_operation_webhook_bindings;
DROP SEQUENCE IF EXISTS customer_operation_webhook_binding_revision_seq;
