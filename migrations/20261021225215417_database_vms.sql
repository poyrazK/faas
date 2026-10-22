-- filename: 20261021225215417_database_vms.sql
-- migration-compat: expand

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
    -- boot_slots caps the database VMs booting on the node at once, so a
    -- wake storm queues instead of starving every boot of CPU.
    boot_slots integer NOT NULL DEFAULT 4
        CHECK (boot_slots BETWEEN 1 AND 256),
    -- The PostgreSQL majors the node has base drives for; empty is any.
    postgres_majors integer[] NOT NULL DEFAULT '{}'
        CHECK (postgres_majors <@ ARRAY[14, 15, 16, 17, 18]),
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
        CHECK (state IN ('provisioning', 'parked', 'waking', 'running', 'parking', 'moving', 'deleting', 'failed')),
    bootstrapped boolean NOT NULL DEFAULT false,
    -- Always-on databases are never parked for idleness, and schedd wakes
    -- them when they are found parked (scale_to_zero = false).
    always_on boolean NOT NULL DEFAULT false,
    -- Last successful base backup (ADR-1027 durability); the scheduler
    -- backs up running databases older than its interval.
    last_backup_at timestamptz,
    -- How far back the customer may restore; WAL and base backups are kept
    -- at least this long.
    restore_window_seconds bigint NOT NULL DEFAULT 0
        CHECK (restore_window_seconds >= 0),
    -- A point-in-time restore's lineage: the database whose archive this
    -- one was recovered from, and the instant. Fixed at placement.
    restore_source uuid,
    restore_target timestamptz,
    -- The restore's last replayed commit, recorded when it finished.
    restore_recovered_to timestamptz,
    -- The schedd replica running a restore holds this lease and renews it.
    restore_lease_until timestamptz,
    -- The shape or volume above changed and the VM has not been restarted
    -- on it yet (a resize in progress).
    resize_pending boolean NOT NULL DEFAULT false,
    -- Stable error code of the last failed transition; never free text.
    last_error text
        CHECK (last_error IS NULL OR last_error ~ '^[a-z_]{1,64}$'),
    last_active_at timestamptz,
    state_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    -- A moving database's volume is being copied to move_node; the schedd
    -- replica running the move holds move_lease_until and renews it.
    move_node text REFERENCES database_nodes (id),
    move_lease_until timestamptz,
    -- Production class: a replicated database keeps a hot standby with
    -- its own volume on standby_node. standby_running: the standby's VM
    -- runs there. standby_synced: the standby holds every acknowledged
    -- commit, so it may be promoted; schedd clears it before a primary
    -- may commit without waiting for its standby. promote_pending: the
    -- volume on node_id is a standby promoted while parked (a failover);
    -- its next boot promotes it onto a new timeline. drop_standby_slot:
    -- the database stopped being replicated, and its primary still holds
    -- the standby's replication slot until it next runs.
    replicated boolean NOT NULL DEFAULT false,
    standby_node text REFERENCES database_nodes (id),
    standby_running boolean NOT NULL DEFAULT false,
    standby_synced boolean NOT NULL DEFAULT false,
    promote_pending boolean NOT NULL DEFAULT false,
    drop_standby_slot boolean NOT NULL DEFAULT false,
    CONSTRAINT database_vms_restore_lineage
        CHECK ((restore_source IS NULL) = (restore_target IS NULL)
            AND restore_source IS DISTINCT FROM database_id),
    CONSTRAINT database_vms_move
        CHECK ((state = 'moving') = (move_node IS NOT NULL)
            AND (move_node IS NULL OR (move_node <> node_id AND move_lease_until IS NOT NULL))),
    CONSTRAINT database_vms_standby
        CHECK ((standby_node IS NULL OR (replicated AND standby_node <> node_id))
            AND (NOT standby_running OR standby_node IS NOT NULL)
            AND (NOT standby_synced OR standby_node IS NOT NULL)
            AND (move_node IS NULL OR standby_node IS NULL OR move_node <> standby_node)
            AND NOT (drop_standby_slot AND replicated))
);

CREATE INDEX IF NOT EXISTS database_vms_node_state_idx
    ON database_vms (node_id, state);

-- Usage of each native database (ADR-1027 metering), written by schedd and
-- kept past the database's deletion so its last windows can be collected.
-- compute, storage and history rows are intervals at a constant
-- per-second rate (vCPUs while the VM runs, provisioned volume bytes,
-- archive bytes); an open interval has no end. egress rows count the bytes
-- pgproxyd sent to clients in the hour starting at started_at.
CREATE TABLE IF NOT EXISTS database_usage (
    database_id uuid NOT NULL,
    meter text NOT NULL
        CHECK (meter IN ('compute', 'storage', 'history', 'egress', 'standby_compute', 'standby_storage')),
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    rate bigint NOT NULL
        CHECK (rate >= 0),
    CONSTRAINT database_usage_interval
        CHECK (ended_at IS NULL OR ended_at >= started_at),
    CONSTRAINT database_usage_egress_open
        CHECK (meter <> 'egress' OR ended_at IS NULL)
);

CREATE INDEX IF NOT EXISTS database_usage_lookup_idx
    ON database_usage (database_id, started_at);

-- At most one open interval per database and rate meter.
CREATE UNIQUE INDEX IF NOT EXISTS database_usage_open_idx
    ON database_usage (database_id, meter) WHERE ended_at IS NULL AND meter <> 'egress';

-- One egress row per database and hour.
CREATE UNIQUE INDEX IF NOT EXISTS database_usage_egress_idx
    ON database_usage (database_id, started_at) WHERE meter = 'egress';

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
DROP TABLE IF EXISTS database_usage;
DROP TABLE IF EXISTS database_role_verifiers;
DROP TABLE IF EXISTS database_vms;
DROP TABLE IF EXISTS database_nodes;
-- +goose StatementEnd
