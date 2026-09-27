-- filename: 20260928101629428_managed_realtime_channel_routes.sql

-- Shared, conservative channel-to-node hints for managed realtime publish.
-- A stale route only adds a node publish; incomplete snapshots never remove
-- rows, and oversized endpoints fall back to full fleet broadcast.

-- +goose Up
create table if not exists managed_realtime_channel_routes (
    endpoint_id uuid not null references managed_realtime_endpoints(id) on delete cascade,
    channel     text not null check (length(channel) between 1 and 256),
    node_id     uuid not null references compute_nodes(id) on delete cascade,
    primary key (endpoint_id, channel, node_id)
);

create table if not exists managed_realtime_channel_route_overflow (
    endpoint_id uuid primary key references managed_realtime_endpoints(id) on delete cascade,
    disabled_at timestamptz not null default now()
);

-- +goose Down
drop table if exists managed_realtime_channel_route_overflow;
drop table if exists managed_realtime_channel_routes;
