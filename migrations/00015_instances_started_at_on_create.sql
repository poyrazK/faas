-- +goose Up
-- +goose StatementBegin
-- Stamp started_at on every new instances row at creation time. The
-- watchdog introduced by commit 3 of the lock-narrowing PR needs
-- started_at on WAKING/COLD_BOOTING rows, but SetInstanceRuntime
-- (pgstore.go:888-890) only runs after a successful vmmd boot — for
-- rows stuck in those states, started_at would be NULL forever.
--
-- A BEFORE INSERT trigger (NOT a column default) is the right tool:
--   - existing NULL rows are NOT modified (preserves audit history
--     for rows that exist today);
--   - every new INSERT picks up started_at = now() automatically,
--     without requiring every CreateInstance call site to remember
--     to set it;
--   - explicit assignments still win (a SetInstanceRuntime or a
--     test fixture can override).
create or replace function instances_started_at_set() returns trigger
  language plpgsql as $$
begin
  if new.started_at is null then
    new.started_at = now();
  end if;
  return new;
end
$$;

drop trigger if exists instances_started_at_set_trg on instances;
create trigger instances_started_at_set_trg
  before insert on instances
  for each row execute function instances_started_at_set();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop trigger if exists instances_started_at_set_trg on instances;
drop function if exists instances_started_at_set();
-- +goose StatementEnd
