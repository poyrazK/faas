-- +goose Up
-- +goose StatementBegin

-- M7: cron firing (spec §4.4). schedd's dispatch loop stamps this column
-- after a synthetic request has been routed through gatewayd so the
-- metering + rate-limit pipeline applies to cron-triggered traffic
-- identically to user traffic. The column is nullable so existing cron
-- rows pre-deploy stay valid; the zero value means "never fired".

alter table crons
  add column if not exists last_fired_at timestamptz;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
alter table crons drop column if exists last_fired_at;
-- +goose StatementEnd
