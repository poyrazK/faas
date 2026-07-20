-- +goose Up
-- Partial index used by schedd's §6.1 watchdog (commit 3 of the
-- lock-narrowing PR). The watchdog ticks every second and runs:
--
--   select ... from instances
--   where state in ('waking','cold_booting','snapshotting')
--     and coalesce(started_at, parked_at) < $1;
--
-- The partial index covers the WHERE clause with state as the leading
-- column and started_at as the comparison column. Without this, the
-- watchdog scans the whole instances table on every tick — fine at
-- one-box scale, but the index costs ~nothing and removes the need
-- for a future operator to think about it when the box grows.
create index if not exists instances_watchdog_state_idx
  on instances (state, started_at)
  where state in ('waking', 'cold_booting', 'snapshotting');
-- +goose Down
drop index if exists instances_watchdog_state_idx;
