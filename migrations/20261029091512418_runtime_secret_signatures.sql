-- filename: 20261029091512418_runtime_secret_signatures.sql
-- migration-compat: expand
-- ADR 20261028161502 amendment 1: apid's Ed25519 writer signature over each
-- runtime secret envelope it seals, keyed by the envelope's SHA-256. The
-- signature covers the ciphertext alone, so every copy of the same bytes (an
-- environment clone, a release snapshot, a cutover) shares the one row. vmmd
-- refuses an envelope whose signature does not verify. apid writes rows at
-- seal time and the operator backfill signs envelopes sealed before that.
--
-- The previous release neither reads nor writes this table.

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS runtime_secret_signatures (
    ciphertext_sha256 bytea       PRIMARY KEY CHECK (octet_length(ciphertext_sha256) = 32),
    signature         bytea       NOT NULL CHECK (octet_length(signature) = 64),
    created_at        timestamptz NOT NULL DEFAULT now() CHECK (isfinite(created_at))
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS runtime_secret_signatures;
