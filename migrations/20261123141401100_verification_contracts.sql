-- filename: 20261123141401100_verification_contracts.sql
-- migration-compat: expand
-- ADR-20261122220000: verification contracts. app_verification_contracts
-- holds each app's gregale.verify.yaml in its validated JSON form; imaged
-- reads it while verifying a candidate. deployment_verification_queries is
-- the handoff for the contract's read-only SQL checks: imaged queues one row
-- per check, apid (which alone holds managed Postgres credentials) runs it
-- as the database's agent query login and writes back only the row count and
-- the first value, and apid deletes rows an hour after they expire. Both
-- tables are new, so the previous release neither reads nor writes them.
-- Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_verification_contracts (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    contract jsonb NOT NULL CHECK (jsonb_typeof(contract) = 'object'),
    contract_sha256 text NOT NULL CHECK (contract_sha256 ~ '^[0-9a-f]{64}$'),
    byte_size integer NOT NULL CHECK (byte_size BETWEEN 1 AND 262144),
    check_count integer NOT NULL CHECK (check_count BETWEEN 1 AND 32),
    revision bigint NOT NULL CHECK (revision >= 1),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE app_verification_contracts IS 'Per-app gregale.verify.yaml contract (validated JSON) run against deployment candidates before promotion (ADR-20261122220000).';

CREATE INDEX IF NOT EXISTS app_verification_contracts_account_idx
    ON app_verification_contracts (account_id);

CREATE TABLE IF NOT EXISTS deployment_verification_queries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL,
    check_name text NOT NULL CHECK (octet_length(check_name) BETWEEN 1 AND 64),
    database_name text NOT NULL CHECK (octet_length(database_name) BETWEEN 1 AND 63),
    query text NOT NULL CHECK (octet_length(query) BETWEEN 1 AND 65536),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done')),
    row_count integer CHECK (row_count >= 0),
    has_first boolean,
    first_value text CHECK (octet_length(first_value) <= 1024),
    error_code text CHECK (octet_length(error_code) <= 64),
    error text CHECK (octet_length(error) <= 512),
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    completed_at timestamptz,
    expires_at timestamptz NOT NULL
);
COMMENT ON TABLE deployment_verification_queries IS 'Read-only SQL checks imaged hands to apid during candidate verification; result is row count and first value only (ADR-20261122220000).';

CREATE INDEX IF NOT EXISTS deployment_verification_queries_pending_idx
    ON deployment_verification_queries (created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS deployment_verification_queries_expires_idx
    ON deployment_verification_queries (expires_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS deployment_verification_queries;
DROP TABLE IF EXISTS app_verification_contracts;
