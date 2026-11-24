-- filename: 20261124144710100_deployment_verification_leases.sql
-- migration-compat: expand
-- ADR-20261122220000 amendment 2: secret references in verification
-- contract headers. imaged writes one lease row naming the node that is
-- verifying a deployment before it asks secretd for the app secrets the
-- contract's headers reference; secretd opens them only for that node, while
-- the lease is unexpired and the deployment is neither live nor terminal.
-- The table is new, so the previous release neither reads nor writes it.
-- Re-runnable.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS deployment_verification_leases (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    node_name text NOT NULL CHECK (octet_length(node_name) BETWEEN 1 AND 253),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE deployment_verification_leases IS 'Node verifying a deployment candidate; secretd opens contract-referenced app secrets only for it (ADR-20261122220000 amendment 2).';

CREATE INDEX IF NOT EXISTS deployment_verification_leases_expires_idx
    ON deployment_verification_leases (expires_at);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS deployment_verification_leases;
