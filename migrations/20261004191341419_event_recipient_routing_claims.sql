-- filename: 20261004191341419_event_recipient_routing_claims.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE event_fanout_outbox ADD COLUMN IF NOT EXISTS recipient_claims boolean NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS event_fanout_recipients (
    outbox_id bigint NOT NULL REFERENCES event_fanout_outbox(id) ON DELETE CASCADE,
    subscription_id text NOT NULL,
    app_id uuid NOT NULL,
    recipient jsonb NOT NULL CHECK (jsonb_typeof(recipient) = 'object'),
    state text NOT NULL CHECK (state IN ('pending', 'processing', 'filtered', 'enqueued', 'failed')),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    total_attempts integer NOT NULL DEFAULT 0 CHECK (total_attempts >= attempts),
    available_at timestamptz NOT NULL DEFAULT now(),
    claim_token uuid,
    lease_until timestamptz,
    PRIMARY KEY (outbox_id, subscription_id),
    CHECK ((state = 'processing' AND claim_token IS NOT NULL AND lease_until IS NOT NULL)
        OR (state <> 'processing' AND claim_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS event_fanout_recipients_due_idx ON event_fanout_recipients
    (available_at, outbox_id, subscription_id) WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS event_fanout_recipients_lease_idx ON event_fanout_recipients
    (lease_until, outbox_id, subscription_id) WHERE state = 'processing';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM event_fanout_outbox WHERE recipient_claims) THEN
        RAISE EXCEPTION 'recipient-owned receipts must expire before rollback; retained receipts require compatible binaries';
    END IF;
END $$;
DROP TABLE IF EXISTS event_fanout_recipients;
ALTER TABLE event_fanout_outbox DROP COLUMN IF EXISTS recipient_claims;
-- +goose StatementEnd
