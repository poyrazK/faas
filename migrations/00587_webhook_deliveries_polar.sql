-- +goose Up
-- +goose StatementBegin

-- Polar uses the same durable webhook replay ledger as the other external
-- webhook providers. Keep the constraint explicit so an unknown provider
-- cannot silently create a namespace that no ingress reads.
alter table webhook_deliveries
    drop constraint if exists webhook_deliveries_provider_check;

alter table webhook_deliveries
    add constraint webhook_deliveries_provider_check
    check (provider in ('github', 'stripe', 'paddle', 'polar', 'resend'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

alter table webhook_deliveries
    drop constraint if exists webhook_deliveries_provider_check;

alter table webhook_deliveries
    add constraint webhook_deliveries_provider_check
    check (provider in ('github', 'stripe', 'paddle'));

-- +goose StatementEnd
