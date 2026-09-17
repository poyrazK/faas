-- Bounded overlap for managed realtime static bearer rotation.

-- +goose Up
alter table managed_realtime_endpoints
    add column if not exists auth_token_previous_sealed bytea not null default ''::bytea,
    add column if not exists auth_token_previous_expires_at timestamptz;

alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_tokens_len_chk;

alter table managed_realtime_endpoints
    add constraint managed_realtime_tokens_len_chk
    check (octet_length(callback_auth_token_sealed) <= 4096
       and octet_length(auth_token_sealed) <= 4096
       and octet_length(auth_token_previous_sealed) <= 4096);

-- +goose Down
alter table managed_realtime_endpoints
    drop constraint if exists managed_realtime_tokens_len_chk;
alter table managed_realtime_endpoints
    add constraint managed_realtime_tokens_len_chk
    check (octet_length(callback_auth_token_sealed) <= 4096
       and octet_length(auth_token_sealed) <= 4096);
alter table managed_realtime_endpoints
    drop column if exists auth_token_previous_sealed,
    drop column if exists auth_token_previous_expires_at;
