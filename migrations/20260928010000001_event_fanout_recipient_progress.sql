-- +goose Up
-- +goose StatementBegin
-- Each accepted candidate keeps its own routing outcome. Invocation-level
-- retries remain on invocations after the event has been enqueued.
ALTER TABLE event_fanout_outbox
    ADD COLUMN recipient_progress jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(recipient_progress) = 'object');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE event_fanout_outbox DROP COLUMN recipient_progress;
-- +goose StatementEnd
