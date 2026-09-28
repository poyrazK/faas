-- filename: 20260928221123698_app_park_webhook_completion.sql

-- +goose Up
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS park_transition_id uuid;

CREATE TABLE IF NOT EXISTS app_park_transitions (
    id             uuid PRIMARY KEY,
    app_id         uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    requested_at   timestamptz NOT NULL DEFAULT now(),
    completed_at   timestamptz,
    superseded_at  timestamptz,
    CONSTRAINT app_park_transitions_terminal_chk
        CHECK (completed_at IS NULL OR superseded_at IS NULL)
);

CREATE INDEX IF NOT EXISTS app_park_transitions_pending_idx
    ON app_park_transitions (requested_at, id)
    WHERE completed_at IS NULL AND superseded_at IS NULL;

ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox
    ADD CONSTRAINT app_webhook_event_outbox_event_chk
        CHECK (event IN ('usage_statement.finalized', 'app.parked'));

-- +goose Down
DELETE FROM app_webhook_event_outbox WHERE event = 'app.parked';
ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox
    ADD CONSTRAINT app_webhook_event_outbox_event_chk
        CHECK (event = 'usage_statement.finalized');
DROP INDEX IF EXISTS app_park_transitions_pending_idx;
DROP TABLE IF EXISTS app_park_transitions;
ALTER TABLE apps DROP COLUMN IF EXISTS park_transition_id;
