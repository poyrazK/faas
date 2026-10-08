-- Principal inbox streams and device checkpoints use dedicated storage.
-- Stream keys are SHA-256 hashes of verified principals.

-- +goose Up
create table if not exists managed_realtime_inbox_heads (
    endpoint_id uuid not null references managed_realtime_endpoints(id) on delete cascade,
    channel text not null check (channel ~ '^[0-9a-f]{64}$'),
    next_sequence bigint not null default 1 check (next_sequence > 0),
    oldest_sequence bigint not null default 1 check (oldest_sequence > 0 and oldest_sequence <= next_sequence),
    primary key (endpoint_id, channel)
);

create table if not exists managed_realtime_inbox_messages (
    endpoint_id uuid not null,
    channel text not null,
    sequence bigint not null check (sequence > 0),
    data bytea not null check (octet_length(data) <= 4096),
    is_binary boolean not null default false,
    idempotency_key text not null check (length(idempotency_key) between 1 and 128),
    created_at timestamptz not null default now(),
    primary key (endpoint_id, channel, sequence),
    foreign key (endpoint_id, channel)
        references managed_realtime_inbox_heads(endpoint_id, channel) on delete cascade
);

create unique index if not exists managed_realtime_inbox_messages_idempotency_idx
    on managed_realtime_inbox_messages(endpoint_id, channel, idempotency_key)
    where idempotency_key is not null;

create index if not exists managed_realtime_inbox_messages_created_at_idx
    on managed_realtime_inbox_messages(created_at);

create table if not exists managed_realtime_inbox_cursors (
    endpoint_id uuid not null references managed_realtime_endpoints(id) on delete cascade,
    principal text not null check (length(principal) between 1 and 256),
    subscription text not null check (length(subscription) between 1 and 128),
    channel text not null check (channel ~ '^[0-9a-f]{64}$'),
    sequence bigint not null default 0 check (sequence >= 0),
    updated_at timestamptz not null default clock_timestamp(),
    primary key (endpoint_id, principal, subscription, channel),
    check (principal = channel)
);

create index if not exists managed_realtime_inbox_cursors_expiry_idx
    on managed_realtime_inbox_cursors(updated_at, endpoint_id);

-- +goose Down
drop table if exists managed_realtime_inbox_cursors;
drop table if exists managed_realtime_inbox_messages;
drop table if exists managed_realtime_inbox_heads;
