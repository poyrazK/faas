-- filename: 20260928221125542_app_wake_webhook_completion.sql

-- +goose Up
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS wake_transition_id uuid;

CREATE TABLE IF NOT EXISTS app_wake_transitions (
    id             uuid PRIMARY KEY,
    app_id         uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    requested_at   timestamptz NOT NULL DEFAULT now(),
    completed_at   timestamptz,
    superseded_at  timestamptz,
    instance_id    uuid,
    wake_id        uuid,
    CONSTRAINT app_wake_transitions_terminal_chk
        CHECK (completed_at IS NULL OR superseded_at IS NULL),
    CONSTRAINT app_wake_transitions_completion_identity_chk
        CHECK ((completed_at IS NULL) = (instance_id IS NULL)
           AND (completed_at IS NULL) = (wake_id IS NULL))
);

CREATE INDEX IF NOT EXISTS app_wake_transitions_pending_idx
    ON app_wake_transitions (requested_at, id)
    WHERE completed_at IS NULL AND superseded_at IS NULL;

ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox
    ADD CONSTRAINT app_webhook_event_outbox_event_chk
        CHECK (event IN ('usage_statement.finalized', 'app.parked', 'app.woken'));

-- +goose Down
DELETE FROM app_webhook_event_outbox WHERE event = 'app.woken';
ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox
    ADD CONSTRAINT app_webhook_event_outbox_event_chk
        CHECK (event IN ('usage_statement.finalized', 'app.parked'));
DROP INDEX IF EXISTS app_wake_transitions_pending_idx;
DROP TABLE IF EXISTS app_wake_transitions;
ALTER TABLE apps DROP COLUMN IF EXISTS wake_transition_id;
