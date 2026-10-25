-- filename: 20261024083056981_honeytokens.sql
-- migration-compat: expand

-- +goose Up
-- Honeytokens (ADR 20261024014500): decoy credentials that belong to no
-- account. Each is stored as an HMAC under apid's host HMAC key, which is not
-- in the database, so a copy of the database cannot tell decoys from real
-- keys. Presenting one is evidence that the place it was planted was read.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS honeytokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         text NOT NULL CHECK (kind IN ('api_key')),
    token_digest text NOT NULL UNIQUE CHECK (token_digest ~ '^[0-9a-f]{64}$'),
    label        text NOT NULL CHECK (length(label) BETWEEN 1 AND 200),
    planted_at   timestamptz NOT NULL DEFAULT now(),
    retired_at   timestamptz
);
-- +goose StatementEnd

COMMENT ON TABLE honeytokens IS
    'Decoy credentials that belong to no account (ADR 20261024014500). Presenting one pages: whoever holds it read the place it was planted.';

-- +goose Down
DROP TABLE IF EXISTS honeytokens;
