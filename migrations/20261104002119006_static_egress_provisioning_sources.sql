-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep legacy readers/writers and their key intact. Each current operator
-- bundle owns a complete set in this new table, including the empty set.
CREATE TABLE IF NOT EXISTS static_egress_provisioning_grants (
    source_id TEXT NOT NULL CHECK (length(source_id) BETWEEN 1 AND 128),
    account_id UUID NOT NULL,
    customer_ip INET NOT NULL CHECK (family(customer_ip) = 4),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, account_id, customer_ip)
);
CREATE INDEX IF NOT EXISTS static_egress_provisioning_account_ip_idx
    ON static_egress_provisioning_grants (account_id, customer_ip);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE static_egress_provisioning_grants;
