-- +goose Up
-- Durable source-side migration leases (Issue #1184 / ADR-137).
--
-- vmmd keeps a small in-memory cache for the paused VM and snapshot
-- handles, but the lease authority must survive a vmmd restart.  The
-- instance row still carries lease_token for the Phase-3 CAS; this table
-- retains the source-side cleanup metadata needed by Phase 4/5 and the
-- expiry watchdog.
create table if not exists migration_leases (
    instance_id          uuid primary key references instances(id) on delete cascade,
    lease_token          text not null unique,
    source_node_id       text not null default '',
    created_at            timestamptz not null,
    lease_expires_at     timestamptz not null,
    mem_storage_key      text not null default '',
    vmstate_storage_key  text not null default '',
    pending               boolean not null default true
);

create index if not exists migration_leases_expiry_idx
    on migration_leases (lease_expires_at, instance_id);

-- +goose Down
drop table if exists migration_leases;
