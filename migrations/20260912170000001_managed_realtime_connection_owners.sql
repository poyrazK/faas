-- Leased connection ownership directory for managed realtime (ADR-156).
-- realtimed remains the socket owner; apid/control-plane processes only
-- coordinate the node that currently owns a connection.

-- +goose Up
create table if not exists managed_realtime_connection_owners (
    connection_id       text primary key,
    endpoint_id         uuid not null references managed_realtime_endpoints(id) on delete cascade,
    node_id             uuid not null references compute_nodes(id) on delete cascade,
    lease_token         text not null,
    lease_expires_at    timestamptz not null,
    updated_at          timestamptz not null default now(),
    constraint managed_realtime_owner_connection_id_chk
        check (length(connection_id) between 1 and 256),
    constraint managed_realtime_owner_token_chk
        check (length(lease_token) between 1 and 128)
);

create index if not exists managed_realtime_connection_owners_endpoint_idx
    on managed_realtime_connection_owners (endpoint_id, lease_expires_at);
create index if not exists managed_realtime_connection_owners_node_idx
    on managed_realtime_connection_owners (node_id, lease_expires_at);

-- +goose Down
drop table if exists managed_realtime_connection_owners;
