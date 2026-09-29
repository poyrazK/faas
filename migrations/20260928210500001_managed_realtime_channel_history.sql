-- Bounded, ordered outbound history for opt-in resumable realtime channels.
-- The head row serializes publishers across apid replicas. The oldest sequence
-- remains meaningful even after every retained message has been removed.

-- +goose Up
create table if not exists managed_realtime_channel_heads (
    endpoint_id uuid not null references managed_realtime_endpoints(id) on delete cascade,
    channel text not null check (length(channel) between 1 and 256),
    next_sequence bigint not null default 1 check (next_sequence > 0),
    oldest_sequence bigint not null default 1 check (oldest_sequence > 0 and oldest_sequence <= next_sequence),
    primary key (endpoint_id, channel)
);

create table if not exists managed_realtime_channel_messages (
    endpoint_id uuid not null,
    channel text not null,
    sequence bigint not null check (sequence > 0),
    data bytea not null check (octet_length(data) <= 4096),
    is_binary boolean not null default false,
    idempotency_key text check (idempotency_key is null or length(idempotency_key) between 1 and 128),
    created_at timestamptz not null default now(),
    primary key (endpoint_id, channel, sequence),
    foreign key (endpoint_id, channel)
        references managed_realtime_channel_heads(endpoint_id, channel) on delete cascade
);

create unique index if not exists managed_realtime_channel_messages_idempotency_idx
    on managed_realtime_channel_messages(endpoint_id, channel, idempotency_key)
    where idempotency_key is not null;

create index if not exists managed_realtime_channel_messages_created_at_idx
    on managed_realtime_channel_messages(created_at);

-- +goose Down
drop table if exists managed_realtime_channel_messages;
drop table if exists managed_realtime_channel_heads;
