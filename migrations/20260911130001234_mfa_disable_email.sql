-- +goose Up
create table if not exists mfa_disable_requests (
  token_hash bytea primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  requested_at timestamptz not null,
  consumed_at timestamptz
);

create index if not exists mfa_disable_requests_account_idx
  on mfa_disable_requests (account_id, requested_at desc)
  where consumed_at is null;

-- +goose Down
drop table if exists mfa_disable_requests;
