-- Scenario chaos plans live with their run-scoped service memberships so
-- deleting the namespace removes the plan atomically. Expiry is set by apid.
-- +goose Up
alter table scenario_test_members
    add column chaos_rules jsonb not null default '[]'::jsonb
        check (jsonb_typeof(chaos_rules) = 'array'),
    add column chaos_expires_at timestamptz;

-- +goose Down
alter table scenario_test_members
    drop column chaos_expires_at,
    drop column chaos_rules;
