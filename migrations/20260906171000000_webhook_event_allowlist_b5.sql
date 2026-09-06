-- filename: 20260906171000000_webhook_event_allowlist_b5.sql
-- +goose Up
-- +goose StatementBegin

-- Issue #1395 B5: close the loop on deployment, rollout, error, job,
-- preview, and budget events. The delivery ledger is already durable;
-- this forward-only widening makes every customer-facing event name
-- insertable and replayable through the existing retry endpoint.
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
            'job.finished', 'preview.created', 'budget.threshold'
        )) not valid;

alter table app_webhook_deliveries
    validate constraint app_webhook_deliveries_event_chk;

-- +goose StatementEnd

-- +goose Down
-- Forward-only: rows carrying a B5 event must remain replayable after a
-- downgrade attempt, so the widened vocabulary is intentionally retained.
-- +goose StatementBegin
select 1;
-- +goose StatementEnd
