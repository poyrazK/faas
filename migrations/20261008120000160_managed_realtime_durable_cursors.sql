-- Server-managed checkpoints for named, authenticated realtime consumers.
-- Inactive checkpoints expire after 30 days; history itself remains subject
-- to the shorter channel retention window.

-- +goose Up
create table if not exists managed_realtime_durable_cursors (
    endpoint_id uuid not null references managed_realtime_endpoints(id) on delete cascade,
    principal text not null check (length(principal) between 1 and 256),
    subscription text not null check (length(subscription) between 1 and 128),
    channel text not null check (length(channel) between 1 and 256),
    sequence bigint not null default 0 check (sequence >= 0),
    updated_at timestamptz not null default clock_timestamp(),
    primary key (endpoint_id, principal, subscription, channel)
);

create index if not exists managed_realtime_durable_cursors_expiry_idx
    on managed_realtime_durable_cursors(updated_at, endpoint_id);

-- +goose Down
drop table if exists managed_realtime_durable_cursors;
