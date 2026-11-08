-- migration-compat: expand
-- filename: 20261108110000000_edge_rule_header_secrets.sql

-- +goose Up
-- Sealed edge-rule request header values (ADR-20261108110000). apid seals a
-- secret header op's value to the edge seal domain and stores it here; the
-- rule keeps only the row id, and gatewayd-internal opens the value when it
-- compiles the rule. Rows never change after insert and go with their app.
-- Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS edge_rule_header_secrets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    ciphertext bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT edge_rule_header_secrets_pkey PRIMARY KEY (id),
    CONSTRAINT edge_rule_header_secrets_ciphertext_check CHECK (octet_length(ciphertext) BETWEEN 1 AND 16384),
    CONSTRAINT edge_rule_header_secrets_created_at_check CHECK (isfinite(created_at))
);
CREATE INDEX IF NOT EXISTS edge_rule_header_secrets_app_id_idx ON edge_rule_header_secrets (app_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS edge_rule_header_secrets;
-- +goose StatementEnd
