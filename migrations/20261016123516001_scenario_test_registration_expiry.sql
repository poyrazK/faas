-- +goose Up
-- The deadline is derived from member preview leases, never caller supplied.
-- NULL means unknown (including registrations made by an older binary).
alter table scenario_test_members add column registration_expires_at timestamptz
    check (registration_expires_at is null or isfinite(registration_expires_at));

update scenario_test_members m set registration_expires_at = a.preview_expires_at
from apps a where a.id = m.app_id and isfinite(a.preview_expires_at);

create index scenario_test_members_expiry_idx on scenario_test_members
    (registration_expires_at, account_id, run_id) where registration_expires_at is not null;

-- +goose Down
drop index if exists scenario_test_members_expiry_idx;
alter table scenario_test_members drop column registration_expires_at;
