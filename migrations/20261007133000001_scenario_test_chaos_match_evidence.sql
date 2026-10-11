-- Keep bounded, per-plan rule-match counts for scenario reports. Gateways
-- aggregate locally and persist batches, so HTTP/TCP fault paths avoid a
-- database write per request or connection.
-- +goose Up
alter table scenario_test_members
    add column if not exists chaos_generation uuid;

create table if not exists scenario_test_chaos_matches (
    account_id uuid not null references accounts(id) on delete cascade,
    run_id text not null check (run_id ~ '^[0-9a-f]{32}$'),
    caller_app_id uuid not null references apps(id) on delete cascade,
    generation uuid not null,
    rule_id text not null check (rule_id ~ '^[0-9a-f]{64}$'),
    matches bigint not null default 0 check (matches >= 0),
    updated_at timestamptz not null default now(),
    primary key (account_id, run_id, caller_app_id, generation, rule_id)
);

create index if not exists scenario_test_chaos_matches_run_idx
    on scenario_test_chaos_matches (account_id, run_id, generation);

-- +goose Down
drop table if exists scenario_test_chaos_matches;
alter table scenario_test_members drop column if exists chaos_generation;
