-- Per-endpoint managed realtime policy (Realtime v2).
-- Zero values preserve the realtimed defaults; non-zero values are bounded by
-- the daemon's global caps before a socket is admitted.

-- +goose Up
alter table managed_realtime_endpoints
    add column if not exists allowed_origins text[] not null default '{}'::text[],
    add column if not exists max_connections integer not null default 0,
    add column if not exists max_message_bytes bigint not null default 0,
    add column if not exists max_connection_age_seconds bigint not null default 0;

alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_endpoint_policy_chk;

alter table managed_realtime_endpoints
    add constraint managed_realtime_endpoint_policy_chk
    check (max_connections between 0 and 10000
       and max_message_bytes between 0 and 1048576
       and max_connection_age_seconds between 0 and 604800
       and cardinality(allowed_origins) <= 16);

-- +goose Down
alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_endpoint_policy_chk;
alter table managed_realtime_endpoints
    drop column if exists allowed_origins,
    drop column if exists max_connections,
    drop column if exists max_message_bytes,
    drop column if exists max_connection_age_seconds;
