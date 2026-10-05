-- +goose Up
CREATE SEQUENCE IF NOT EXISTS workflow_webhook_binding_revision_seq;
CREATE TABLE IF NOT EXISTS workflow_webhook_bindings (
    endpoint_id uuid PRIMARY KEY REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    workflow_name text NOT NULL CHECK (octet_length(workflow_name) BETWEEN 1 AND 128),
    event_type text NOT NULL CHECK (event_type ~ '^[a-z*][a-z0-9_.*]{0,255}$'),
    filter jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(filter) = 'object'),
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS workflow_webhook_receipts (
    endpoint_id uuid NOT NULL REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    provider_event_id text NOT NULL CHECK (octet_length(provider_event_id) BETWEEN 1 AND 256),
    receipt_id uuid NOT NULL UNIQUE,
    body_hash bytea NOT NULL CHECK (octet_length(body_hash) = 32),
    workflow_name text NOT NULL CHECK (octet_length(workflow_name) BETWEEN 1 AND 128),
    recipient_id uuid,
    outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('accepted', 'ignored')),
    ignored_reason text CHECK (ignored_reason IN ('automation_paused', 'event_filtered', 'automation_unpublished')),
    accepted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(endpoint_id, provider_event_id),
    CHECK ((status = 'accepted' AND recipient_id IS NOT NULL AND ignored_reason IS NULL)
        OR (status = 'ignored' AND recipient_id IS NULL AND ignored_reason IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS workflow_webhook_receipts_outbox_idx ON workflow_webhook_receipts(outbox_id);
-- Serialize competing routing modes even when an older API binary writes a
-- callback or managed-operation binding. One endpoint has one delivery mode.
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
           OR EXISTS (SELECT 1 FROM exclusive_work_trigger_bindings WHERE source = 'inbound_webhook' AND trigger_id = endpoint) THEN
            RAISE EXCEPTION 'endpoint already has a callback or operation binding' USING ERRCODE = '23505';
        END IF;
    ELSIF EXISTS (SELECT 1 FROM workflow_webhook_bindings WHERE endpoint_id = endpoint) THEN
        RAISE EXCEPTION 'endpoint already starts an automation' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS workflow_webhook_routing_guard ON workflow_webhook_bindings;
CREATE TRIGGER workflow_webhook_routing_guard BEFORE INSERT OR UPDATE ON workflow_webhook_bindings
FOR EACH ROW EXECUTE FUNCTION guard_workflow_webhook_routing();
DROP TRIGGER IF EXISTS workflow_callback_routing_guard ON workflow_callback_webhook_bindings;
CREATE TRIGGER workflow_callback_routing_guard BEFORE INSERT OR UPDATE ON workflow_callback_webhook_bindings
FOR EACH ROW EXECUTE FUNCTION guard_workflow_webhook_routing();
DROP TRIGGER IF EXISTS exclusive_webhook_routing_guard ON exclusive_work_trigger_bindings;
CREATE TRIGGER exclusive_webhook_routing_guard BEFORE INSERT OR UPDATE ON exclusive_work_trigger_bindings
FOR EACH ROW EXECUTE FUNCTION guard_workflow_webhook_routing();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER exclusive_webhook_routing_guard ON exclusive_work_trigger_bindings;
DROP TRIGGER workflow_callback_routing_guard ON workflow_callback_webhook_bindings;
DROP TRIGGER workflow_webhook_routing_guard ON workflow_webhook_bindings;
DROP FUNCTION guard_workflow_webhook_routing();
DROP TABLE workflow_webhook_receipts;
DROP TABLE workflow_webhook_bindings;
DROP SEQUENCE workflow_webhook_binding_revision_seq;
