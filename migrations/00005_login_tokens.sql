-- +goose Up
-- +goose StatementBegin

-- M7.5: magic-link login tokens (spec §14 M7.5, ADR-011 dashboard).
-- A user posts their email to /login; apid mints a 32-byte random
-- token, stores its SHA-256 hash here, and emails the raw token back
-- as a one-shot URL. /auth/verify?token=… hashes it, looks the row up
-- here, marks it consumed, and mints a session cookie.
--
-- consumed_at is set in the same statement that issues the session, so
-- a double-click on the link returns 410 Gone (idempotent + safe).
-- expires_at is a server-side cap; the cleanup goroutine (slice 3+) or
-- Postgres's autovacuum reclaims old rows.

create table if not exists login_tokens (
  token_hash   bytea        primary key,
  account_id   uuid         not null references accounts(id) on delete cascade,
  expires_at   timestamptz  not null,
  consumed_at  timestamptz
);

create index if not exists login_tokens_account_idx
  on login_tokens (account_id, expires_at);

-- +goose StatementEnd
