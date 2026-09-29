-- filename: 20260929004221772_app_webhook_event_outbox.sql

-- +goose Up
-- The producer commits an event and its recipient snapshot with the source
-- mutation. Relays insert all delivery rows and delete the outbox row in one
-- transaction, so a crash can neither lose nor partially fan out the event.
CREATE TABLE IF NOT EXISTS app_webhook_event_outbox (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id            uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id                uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    event                 text NOT NULL,
    source_id             uuid NOT NULL,
    payload               jsonb NOT NULL,
    recipient_webhook_ids uuid[] NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_webhook_event_outbox_event_chk CHECK (event = 'usage_statement.finalized'),
    CONSTRAINT app_webhook_event_outbox_payload_chk CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT app_webhook_event_outbox_recipients_chk CHECK (cardinality(recipient_webhook_ids) > 0),
    CONSTRAINT app_webhook_event_outbox_source_uniq UNIQUE (event, source_id)
);

CREATE INDEX IF NOT EXISTS app_webhook_event_outbox_pending_idx
    ON app_webhook_event_outbox (created_at, id);

ALTER TABLE app_webhook_deliveries
    ADD COLUMN IF NOT EXISTS source_event_id uuid;
CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_source_event_webhook_uniq
    ON app_webhook_deliveries (source_event_id, webhook_id)
    WHERE source_event_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS app_webhook_deliveries_source_event_webhook_uniq;
ALTER TABLE app_webhook_deliveries DROP COLUMN IF EXISTS source_event_id;
DROP TABLE IF EXISTS app_webhook_event_outbox;
