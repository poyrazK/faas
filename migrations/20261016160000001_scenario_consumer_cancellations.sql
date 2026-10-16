-- +goose Up
create table scenario_consumer_cancellations (
    account_id uuid not null references accounts(id) on delete cascade,
    app_id uuid not null references apps(id) on delete cascade,
    external_ref text not null check (length(external_ref) between 1 and 256),
    intent_sha256 text not null check (intent_sha256 ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz not null check (isfinite(expires_at)),
    cancelled_at timestamptz not null default clock_timestamp() check (isfinite(cancelled_at)),
    primary key (account_id, app_id, external_ref)
);

-- +goose Down
drop table scenario_consumer_cancellations;
