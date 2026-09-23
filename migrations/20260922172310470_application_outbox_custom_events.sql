-- filename: 20260922172310470_application_outbox_custom_events.sql

-- +goose Up
-- +goose StatementBegin
-- Application outbox deliveries carry developer-defined event types. The
-- closed vocabulary remains enforced on webhook subscription filters (the
-- platform-generated fan-out surface); explicitly addressed outbox rows need
-- the same bounded CloudEvents-style type contract as the inbox.
alter table app_webhook_deliveries
    drop constraint if exists app_webhook_deliveries_event_chk;

alter table app_webhook_deliveries
    add constraint app_webhook_deliveries_event_chk
        check (char_length(event) between 1 and 256) not valid;

alter table app_webhook_deliveries
    validate constraint app_webhook_deliveries_event_chk;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: restoring the historical allowlist could invalidate custom
-- outbox deliveries that must remain visible and replayable.
select 1;
-- +goose StatementEnd
