-- filename: 20261005085250802_managed_postgres_accounting_reconciliations.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-591: catalog repair and coverage reset retain the operator's evidence.
CREATE TABLE IF NOT EXISTS managed_postgres_accounting_reconciliations (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    reconciliation_id uuid NOT NULL,
    database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id) DEFERRABLE INITIALLY DEFERRED,
    backend_id text NOT NULL,
    backend_fingerprint text NOT NULL,
    provider_resource_id text NOT NULL,
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 256),
    reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 512),
    evidence_reference text NOT NULL CHECK (octet_length(evidence_reference) BETWEEN 1 AND 256),
    evidence_sha256 text NOT NULL CHECK (evidence_sha256 ~ '^[a-f0-9]{64}$'),
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[a-f0-9]{64}$'),
    preview_revision text NOT NULL CHECK (preview_revision ~ '^[a-f0-9]{64}$'),
    request jsonb NOT NULL,
    policy jsonb NOT NULL,
    before_catalog jsonb NOT NULL,
    after_catalog jsonb NOT NULL,
    coverage_before jsonb NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, reconciliation_id),
    UNIQUE (backend_id, backend_fingerprint, provider_resource_id)
);
CREATE OR REPLACE FUNCTION protect_managed_postgres_accounting_reconciliation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND NOT EXISTS (SELECT 1 FROM accounts WHERE id = OLD.account_id) THEN RETURN OLD; END IF;
    RAISE EXCEPTION 'managed postgres accounting reconciliation evidence is append-only';
END $$;
DROP TRIGGER IF EXISTS managed_postgres_accounting_reconciliation_immutable ON managed_postgres_accounting_reconciliations;
CREATE TRIGGER managed_postgres_accounting_reconciliation_immutable BEFORE UPDATE OR DELETE ON managed_postgres_accounting_reconciliations
    FOR EACH ROW EXECUTE FUNCTION protect_managed_postgres_accounting_reconciliation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM managed_postgres_accounting_reconciliations) THEN
        RAISE EXCEPTION 'cannot remove retained managed postgres accounting reconciliation evidence';
    END IF;
END $$;
DROP TABLE managed_postgres_accounting_reconciliations;
DROP FUNCTION protect_managed_postgres_accounting_reconciliation();
-- +goose StatementEnd
