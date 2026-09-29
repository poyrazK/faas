-- +goose Up
-- A scenario run owns an explicit service namespace. The app FK keeps the
-- membership from outliving hard deletion; soft-deleted apps remain fenced.
create table scenario_test_members (
    account_id uuid not null references accounts(id) on delete cascade,
    run_id text not null check (run_id ~ '^[0-9a-f]{32}$'),
    workload_name text not null check (workload_name ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'),
    app_id uuid not null unique references apps(id) on delete cascade,
    primary key (account_id, run_id, workload_name)
);

create index scenario_test_members_run_idx on scenario_test_members (account_id, run_id);

-- +goose Down
drop table scenario_test_members;
