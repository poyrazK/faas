-- Fleet-wide presence snapshots use short leases. Expired members disappear
-- from snapshots even when a realtime node or client connection fails.

-- +goose Up
create table if not exists managed_realtime_presence_leases (
    endpoint_id   uuid not null references managed_realtime_endpoints(id) on delete cascade,
    channel       text not null check (length(channel) between 1 and 256),
    node_id       uuid not null references compute_nodes(id) on delete cascade,
    connection_id text not null check (length(connection_id) between 1 and 128),
    member_id     text not null check (length(member_id) between 1 and 128),
    principal     text not null default '' check (length(principal) <= 256),
    state         jsonb not null check (jsonb_typeof(state) = 'object'),
    expires_at    timestamptz not null,
    updated_at    timestamptz not null default clock_timestamp(),
    state_updated_at timestamptz not null default clock_timestamp(),
    primary key (endpoint_id, channel, node_id, connection_id)
);

create index if not exists managed_realtime_presence_leases_expiry_idx
    on managed_realtime_presence_leases(expires_at);

create index if not exists managed_realtime_presence_principal_idx
    on managed_realtime_presence_leases(endpoint_id, channel, principal, updated_at desc)
    where principal <> '';

-- +goose Down
drop table if exists managed_realtime_presence_leases;
