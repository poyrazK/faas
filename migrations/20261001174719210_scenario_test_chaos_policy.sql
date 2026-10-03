-- Scenario chaos plans live with their run-scoped service memberships so
-- deleting the namespace removes the plan atomically. Expiry is set by apid.
-- +goose Up
alter table scenario_test_members
    add column if not exists chaos_rules jsonb not null default '[]'::jsonb,
    add column if not exists chaos_expires_at timestamptz;

-- +goose StatementBegin
do $$
begin
    if not exists (
        select 1 from pg_constraint
        where conrelid = 'scenario_test_members'::regclass
          and conname = 'scenario_test_members_chaos_rules_array_check'
    ) then
        alter table scenario_test_members
            add constraint scenario_test_members_chaos_rules_array_check
            check (jsonb_typeof(chaos_rules) = 'array');
    end if;
end
$$;
-- +goose StatementEnd

-- +goose Down
alter table scenario_test_members
    drop constraint if exists scenario_test_members_chaos_rules_array_check,
    drop column if exists chaos_expires_at,
    drop column if exists chaos_rules;
