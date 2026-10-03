-- +goose Up
-- +goose StatementBegin
-- Each accepted candidate keeps its own routing outcome. Invocation-level
-- retries remain on invocations after the event has been enqueued.
ALTER TABLE event_fanout_outbox
    ADD COLUMN IF NOT EXISTS recipient_progress jsonb NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        -- Keep PostgreSQL's conventional name so this also recognizes the
        -- constraint created by the original inline CHECK form.
        WHERE conname = 'event_fanout_outbox_recipient_progress_check'
          AND conrelid = 'event_fanout_outbox'::regclass
    ) THEN
        ALTER TABLE event_fanout_outbox
            ADD CONSTRAINT event_fanout_outbox_recipient_progress_check
            CHECK (jsonb_typeof(recipient_progress) = 'object');
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE event_fanout_outbox
    DROP CONSTRAINT IF EXISTS event_fanout_outbox_recipient_progress_check;
ALTER TABLE event_fanout_outbox DROP COLUMN IF EXISTS recipient_progress;
-- +goose StatementEnd
