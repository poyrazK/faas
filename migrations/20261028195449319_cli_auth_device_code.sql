-- filename: 20261028195449319_cli_auth_device_code.sql
-- migration-compat: expand
-- Bug hunt 2026-10-28 H67: the 8-hex-character code a user approves was also
-- the only secret POST /v1/cli-auth/exchange needed, so guessing a pending
-- code won the user's 30-day admin key. A CLI that asks for it now also gets
-- a 32-byte device code only it holds; device_hash stores its SHA-256, and a
-- row with one can be exchanged only with that device code. Rows from older
-- CLIs keep a NULL device_hash and the code-only exchange.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE cli_auth_codes ADD COLUMN IF NOT EXISTS device_hash bytea;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE cli_auth_codes DROP COLUMN IF EXISTS device_hash;
