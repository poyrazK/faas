-- filename: 20261012160131819_database_vms.sql

-- +goose Up
-- +goose StatementBegin

-- ADR-1027: native managed PostgreSQL runs one Firecracker VM per database
-- on dedicated database nodes. database_nodes is their inventory; app
-- placement never reads it, so app VMs cannot land on a database node.
-- database_vms is the runtime state of each native database and is written
-- only by schedd. Customer intent stays in managed_postgres_databases (apid).
CREATE TABLE IF NOT EXISTS database_nodes (
    id text PRIMARY KEY
        CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    target_url text NOT NULL
        CHECK (target_url <> ''),
    admission_ceiling_mb integer NOT NULL
        CHECK (admission_ceiling_mb > 0),
    volume_capacity_bytes bigint NOT NULL
        CHECK (volume_capacity_bytes > 0),
    lifecycle text NOT NULL DEFAULT 'active'
        CHECK (lifecycle IN ('active', 'draining', 'unavailable')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS database_vms (
    database_id uuid PRIMARY KEY,
    node_id text NOT NULL REFERENCES database_nodes (id),
    postgres_major integer NOT NULL
        CHECK (postgres_major BETWEEN 14 AND 18),
    vcpu_count integer NOT NULL
        CHECK (vcpu_count BETWEEN 1 AND 32),
    mem_mib integer NOT NULL
        CHECK (mem_mib BETWEEN 128 AND 65536),
    volume_bytes bigint NOT NULL
        CHECK (volume_bytes > 0),
    state text NOT NULL
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'deleting', 'failed')),
    bootstrapped boolean NOT NULL DEFAULT false,
    -- Always-on databases are never parked for idleness, and schedd wakes
    -- them when they are found parked (scale_to_zero = false).
    always_on boolean NOT NULL DEFAULT false,
    -- Stable error code of the last failed transition; never free text.
    last_error text
        CHECK (last_error IS NULL OR last_error ~ '^[a-z_]{1,64}$'),
    last_active_at timestamptz,
    state_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS database_vms_node_state_idx
    ON database_vms (node_id, state);

-- The SCRAM-SHA-256 verifier of each customer role, in pg_authid format.
-- apid writes it when it sets the role's password inside the VM; pgproxyd
-- reads it to authenticate a client before waking the database and then
-- logs in with the client's proven key. No password is ever stored.
CREATE TABLE IF NOT EXISTS database_role_verifiers (
    database_id uuid NOT NULL,
    role_name text NOT NULL
        CHECK (role_name ~ '^[a-z_][a-z0-9_]{0,62}$'),
    verifier text NOT NULL
        CHECK (verifier ~ '^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/=]+\$[A-Za-z0-9+/=]+:[A-Za-z0-9+/=]+$'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (database_id, role_name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS database_role_verifiers;
DROP TABLE IF EXISTS database_vms;
DROP TABLE IF EXISTS database_nodes;
-- +goose StatementEnd
