-- +goose Up
-- +goose StatementBegin

-- Polar order.created/order.paid projections share the invoice history
-- table with the existing providers. Keep the provider vocabulary in sync
-- with state.Invoice and the webhook upsert path.
alter table invoices drop constraint if exists invoices_provider_check;
alter table invoices
  add constraint invoices_provider_check
  check (provider in ('stripe', 'paddle', 'polar'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table invoices drop constraint if exists invoices_provider_check;
alter table invoices
  add constraint invoices_provider_check
  check (provider in ('stripe', 'paddle'));
-- +goose StatementEnd
