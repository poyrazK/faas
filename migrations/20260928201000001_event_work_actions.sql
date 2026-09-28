-- +goose Up
-- +goose StatementBegin
alter table event_subscription_work_bindings
  add column action text not null default 'invoke'
  check (action in ('invoke', 'cancel_pending'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table event_subscription_work_bindings drop column action;
-- +goose StatementEnd
