-- migration-compat: expand
-- filename: 20261104221112135_webhook_callback_receipts.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS workflow_callback_webhook_receipts (
    endpoint_id uuid NOT NULL REFERENCES inbound_webhook_endpoints(id) ON DELETE CASCADE,
    provider_event_id text NOT NULL CHECK (octet_length(provider_event_id) BETWEEN 1 AND 256),
    body_hash bytea NOT NULL CHECK (octet_length(body_hash)=32),
    callback_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('received','ignored')),
    accepted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (endpoint_id, provider_event_id)
);
CREATE INDEX IF NOT EXISTS workflow_callback_webhook_receipts_retention_idx ON workflow_callback_webhook_receipts (accepted_at,endpoint_id,provider_event_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM workflow_callback_webhook_receipts) THEN
        RAISE EXCEPTION 'cannot remove retained workflow callback webhook receipts';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE IF EXISTS workflow_callback_webhook_receipts;
