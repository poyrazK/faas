-- filename: 20261004235029907_managed_postgres_usage_imports.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-583: operator repairs and their before/after evidence commit with usage.
CREATE TABLE IF NOT EXISTS managed_postgres_usage_imports (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    import_id uuid NOT NULL,
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) DEFERRABLE INITIALLY DEFERRED,
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 256),
    reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 512),
    evidence_reference text NOT NULL CHECK (octet_length(evidence_reference) BETWEEN 1 AND 256),
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[a-f0-9]{64}$'),
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    preview_revision text NOT NULL CHECK (preview_revision ~ '^[a-f0-9]{64}$'),
    request jsonb NOT NULL,
    policy jsonb NOT NULL,
    before_records jsonb NOT NULL,
    after_records jsonb NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, import_id)
);

CREATE OR REPLACE FUNCTION protect_managed_postgres_usage_import() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'managed postgres usage import evidence is append-only';
END $$;
DROP TRIGGER IF EXISTS managed_postgres_usage_import_immutable ON managed_postgres_usage_imports;
CREATE TRIGGER managed_postgres_usage_import_immutable BEFORE UPDATE OR DELETE ON managed_postgres_usage_imports
    FOR EACH ROW EXECUTE FUNCTION protect_managed_postgres_usage_import();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM managed_postgres_usage_imports) THEN
        RAISE EXCEPTION 'cannot remove retained managed postgres usage import evidence';
    END IF;
END $$;
DROP TABLE managed_postgres_usage_imports;
DROP FUNCTION protect_managed_postgres_usage_import();
-- +goose StatementEnd
