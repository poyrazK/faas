-- migration-compat: expand
-- filename: 20261026122057626_managed_postgres_account_overage.sql

-- ADR-20261025094915 decision 2: a paid account may let its managed
-- PostgreSQL databases keep running past the plan's monthly compute
-- allowance, up to twice it, before the cap suspends them. Off is the
-- default, and an account without a row is off. apid is the only writer;
-- the row is keyed by the resource namespace the usage ledger pools by, and
-- goes with it when the namespace is erased.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS managed_postgres_account_overage (
    subject_id uuid PRIMARY KEY REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    enabled boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE managed_postgres_account_overage IS 'Whether a paid account runs its managed PostgreSQL databases past the monthly compute allowance, up to 2x, before suspension; written only by apid (ADR-20261025094915).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS managed_postgres_account_overage;
