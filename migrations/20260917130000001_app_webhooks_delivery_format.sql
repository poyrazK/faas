-- +goose Up
-- +goose StatementBegin

-- CloudEvents 1.0 structured delivery is opt-in. Existing subscriptions stay
-- on the historical Gregale JSON envelope so receivers are not silently
-- broken by a platform upgrade.
alter table app_webhooks
    add column if not exists delivery_format text not null default 'json';

alter table app_webhooks
    drop constraint if exists app_webhooks_delivery_format_chk;

alter table app_webhooks
    add constraint app_webhooks_delivery_format_chk
        check (delivery_format in ('json', 'cloudevents')) not valid;

alter table app_webhooks
    validate constraint app_webhooks_delivery_format_chk;

-- +goose StatementEnd

-- +goose Down
-- Forward-only: changing a subscription's wire format is reversible through
-- the API, but dropping the column would discard that customer contract.
-- +goose StatementBegin
select 1;
-- +goose StatementEnd
