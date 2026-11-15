-- filename: 20261115113839815_cli_auth_verifier_hash.sql
-- migration-compat: expand
-- Account audit F10-3: a one-click (loopback) login hands its device code to
-- http://127.0.0.1:<port>/callback through the approving browser, so the code
-- can land in browser history, a local server's request log or an extension.
-- A CLI that asks for a code now also sends the SHA-256 of a secret verifier
-- it keeps in memory; verifier_hash stores it, and a row with one is
-- exchanged only together with that verifier. Rows from older CLIs keep a
-- NULL verifier_hash and the device-code-only exchange.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE cli_auth_codes ADD COLUMN IF NOT EXISTS verifier_hash bytea;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE cli_auth_codes DROP COLUMN IF EXISTS verifier_hash;
