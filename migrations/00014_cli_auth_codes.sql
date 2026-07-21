-- +goose Up
-- +goose StatementBegin

-- M7.5+: CLI auth codes (spec §2.2 device-code flow). The CLI mints
-- one of these anonymously; the user pastes the human-readable code
-- into the browser at /cli-auth; the dashboard page binds the code to
-- an account_id (creating the account if needed) and the CLI polls
-- /v1/cli-auth/exchange for the plaintext API key.
--
-- The token_hash column is sha256(uppercase(normalized_code)) with the
-- dash stripped — the wire form is "XXXX-NNNN" but the server hashes
-- the 8 hex chars. account_id is NULL between mint and consume; the
-- claim statement fills it in atomically. consumed_at is set in the
-- same statement that mints the API key, so a double-poll after
-- activation returns 410 Gone (idempotent + safe).
--
-- The 4-byte entropy (32 bits) is fine: the consume path is
-- rate-limited to 10/min/IP and the TTL is 5 min, so brute-force on
-- the code space is not realistic.

create table if not exists cli_auth_codes (
  token_hash  bytea        primary key,
  account_id  uuid         references accounts(id) on delete cascade,
  status      text         not null default 'pending'
                            check (status in ('pending','consumed','expired')),
  expires_at  timestamptz  not null,
  consumed_at timestamptz,
  created_at  timestamptz  not null default now()
);

create index if not exists cli_auth_codes_pending_idx
  on cli_auth_codes (status, expires_at)
  where status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists cli_auth_codes_pending_idx;
drop table if exists cli_auth_codes;
-- +goose StatementEnd