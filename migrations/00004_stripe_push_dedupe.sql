-- +goose Up
-- +goose StatementBegin

-- M7: Stripe push dedupe table (spec §4.7, ADR-010). meterd's hourly
-- loop consults this BEFORE the Stripe call so a redelivered hour is a
-- no-op. (The Stripe idempotency-key sits on top for retry safety; this
-- table is OUR dedupe — the box won't double-bill even if Stripe ever
-- loses an idempotency-key window.)
--
-- Schema-only change; no rows are seeded. Rows are inserted by the
-- meterd loop after a successful Stripe call.

create table if not exists stripe_push_dedupe (
  account_id  uuid        not null references accounts(id) on delete cascade,
  hour        timestamptz not null,
  pushed_at   timestamptz not null default now(),
  primary key (account_id, hour)
);

create index if not exists stripe_push_dedupe_hour_idx
  on stripe_push_dedupe (hour);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
drop table if exists stripe_push_dedupe;
-- +goose StatementEnd
