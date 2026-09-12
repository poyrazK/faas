-- Managed realtime endpoint control-plane resources (ADR-156).
-- Live sockets remain owned by realtimed; this table is the durable source of
-- truth apid can reconcile onto whichever realtime node owns a connection.

-- +goose Up
create table if not exists managed_realtime_endpoints (
    id                          uuid primary key default gen_random_uuid(),
    app_id                      uuid not null references apps(id) on delete cascade,
    account_id                  uuid not null references accounts(id) on delete cascade,
    callback_url                text not null,
    connect_path                text not null default '/realtime/connect',
    message_path                text not null default '/realtime/message',
    disconnect_path             text not null default '/realtime/disconnect',
    callback_auth_token_sealed  bytea not null,
    auth_token_sealed           bytea not null default ''::bytea,
    enabled                     boolean not null default true,
    created_at                  timestamptz not null default now(),
    updated_at                  timestamptz not null default now(),
    constraint managed_realtime_callback_url_len_chk
        check (length(callback_url) between 8 and 2048),
    constraint managed_realtime_paths_len_chk
        check (length(connect_path) between 1 and 256
           and length(message_path) between 1 and 256
           and length(disconnect_path) between 1 and 256),
    constraint managed_realtime_paths_shape_chk
        check (left(connect_path, 1) = '/' and left(message_path, 1) = '/'
           and left(disconnect_path, 1) = '/'),
    constraint managed_realtime_tokens_len_chk
        check (octet_length(callback_auth_token_sealed) <= 4096
           and octet_length(auth_token_sealed) <= 4096)
);

create index if not exists managed_realtime_endpoints_app_idx
    on managed_realtime_endpoints (app_id, created_at desc);
create index if not exists managed_realtime_endpoints_account_idx
    on managed_realtime_endpoints (account_id, created_at desc);

-- +goose Down
drop table if exists managed_realtime_endpoints;
