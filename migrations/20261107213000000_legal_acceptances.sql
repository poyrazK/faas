-- filename: 20261107213000000_legal_acceptances.sql

-- legal_acceptances: which version of a legal document (pkg/legal) an
-- account accepted, when, and through which sign-up method. Self-service
-- sign-up writes the Terms of Service row in the same transaction that
-- creates the account, so every account created while the Terms are
-- published can show what it agreed to. A new Terms version adds a row;
-- rows are never updated. Rows go with the account on erasure.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '500ms';
CREATE TABLE IF NOT EXISTS legal_acceptances (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 document text NOT NULL CHECK (document IN ('terms')),
 version text NOT NULL CHECK (version ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'),
 method text NOT NULL CHECK (method <> '' AND length(method) <= 32),
 accepted_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (account_id, document, version)
);
-- +goose Down
DROP TABLE legal_acceptances;
