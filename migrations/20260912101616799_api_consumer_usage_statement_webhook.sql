-- filename: 20260912101616799_api_consumer_usage_statement_webhook.sql

-- +goose Up
-- +goose StatementBegin
-- API consumer usage statements are customer-owned billing facts. Once a
-- statement is finalized, enqueueing this event lets the customer invoice
-- its own API through the existing durable, signed, retryable webhook path.
alter table app_webhook_deliveries
    drop constraint if exists app_webhook_deliveries_event_chk;

alter table app_webhook_deliveries
    add constraint app_webhook_deliveries_event_chk
        check (event in (
            'cron.fired', 'cron.fired.manually',
            'app.created', 'app.deleted', 'app.deployed', 'app.scaled',
            'app.parked', 'app.woken',
            'build.succeeded', 'build.failed',
            'deployment.failed', 'rollout.aborted', 'error.new',
            'job.finished', 'preview.created', 'budget.threshold',
            'usage_statement.finalized'
        )) not valid;

alter table app_webhook_deliveries
    validate constraint app_webhook_deliveries_event_chk;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: finalized statements must remain replayable after a
-- downgrade attempt, so retain the widened event vocabulary.
select 1;
-- +goose StatementEnd
