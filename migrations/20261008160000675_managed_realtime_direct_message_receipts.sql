-- Optional principal-message receipts retain bounded acknowledgement state
-- for one hour. Endpoint deletion removes its receipt and delivery records.

-- +goose Up
create table if not exists managed_realtime_direct_message_receipts (
    endpoint_id          uuid not null references managed_realtime_endpoints(id) on delete cascade,
    message_id           text not null check (length(message_id) between 1 and 128),
    payload_fingerprint  bytea not null check (octet_length(payload_fingerprint) = 32),
    created_at           timestamptz not null default clock_timestamp(),
    expires_at           timestamptz not null,
    dispatch_lease_until timestamptz not null,
    dispatch_complete    boolean not null default false,
    summary              jsonb not null default '{}'::jsonb check (jsonb_typeof(summary) = 'object'),
    primary key (endpoint_id, message_id)
);

create index if not exists managed_realtime_direct_message_receipts_expiry_idx
    on managed_realtime_direct_message_receipts(expires_at);

create table if not exists managed_realtime_direct_message_deliveries (
    endpoint_id      uuid not null,
    message_id       text not null,
    connection_id    text not null check (length(connection_id) between 1 and 128),
    node_id          uuid not null,
    ack_supported    boolean not null,
    queue_status     text not null check (queue_status in ('pending', 'queued', 'unsupported', 'queue_full', 'failed')),
    created_at       timestamptz not null default clock_timestamp(),
    queued_at        timestamptz,
    acknowledged_at  timestamptz,
    primary key (endpoint_id, message_id, connection_id),
    foreign key (endpoint_id, message_id)
        references managed_realtime_direct_message_receipts(endpoint_id, message_id) on delete cascade,
    check (acknowledged_at is null or ack_supported)
);

create index if not exists managed_realtime_direct_message_deliveries_node_idx
    on managed_realtime_direct_message_deliveries(endpoint_id, message_id, node_id);

-- +goose Down
drop table if exists managed_realtime_direct_message_deliveries;
drop table if exists managed_realtime_direct_message_receipts;
