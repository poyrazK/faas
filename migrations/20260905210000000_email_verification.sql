-- +goose Up
-- +goose StatementBegin

-- Password signups start unverified. Existing accounts are backfilled as
-- verified so this migration does not lock out customers who joined before
-- the verification flow existed. OAuth and magic-link account creation mark
-- the account verified through the application layer.
alter table accounts
  add column if not exists email_verified_at timestamptz;

update accounts
set email_verified_at = created_at
where email_verified_at is null;

-- Verification tokens are deliberately separate from login_tokens. Consuming
-- one proves ownership of the address but must not mint a session; the browser
-- confirmation page asks the customer to sign in again.
create table if not exists email_verification_tokens (
  token_hash  bytea       primary key,
  account_id  uuid        not null references accounts(id) on delete cascade,
  expires_at  timestamptz not null,
  consumed_at timestamptz,
  created_at  timestamptz not null default now()
);

create index if not exists email_verification_tokens_account_idx
  on email_verification_tokens (account_id, expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

drop table if exists email_verification_tokens;
alter table accounts drop column if exists email_verified_at;

-- +goose StatementEnd
