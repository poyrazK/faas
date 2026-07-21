-- +goose Up
-- +goose StatementBegin
-- PR-#74 (spec §17 follow-up): schedd's §6.1 watchdog (migration 00016
-- + commit 877de66) moves stuck rows to STOPPED / FAILED. Without a
-- retention sweep those rows accumulate forever, so the daily sweep
-- needs a stable timestamp to age against. started_at was stamped on
-- INSERT by migration 00015 and means "row creation", which is wrong
-- for a STOPPED row that successfully booted (started_at would be
-- days old). parked_at is overloaded (also means "entered PARKED").
--
-- terminal_at is the dedicated anchor: stamped by Engine.transition on
-- the same UPDATE that writes state = 'stopped' or 'failed'. NULL on
-- every row currently in {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING,
-- PARKED}.
alter table instances
  add column if not exists terminal_at timestamptz;

-- Backfill existing terminal rows so the first sweep after deploy
-- doesn't give them a free 30-day grace period. Best-effort anchor:
-- coalesce the existing clocks. Falls back to now() on legacy rows
-- that have neither (one-box only has these on parked/stopped rows
-- from the v1 schema pre-00015).
update instances
  set terminal_at = coalesce(parked_at, started_at, now())
  where state in ('stopped', 'failed')
    and terminal_at is null;

-- Partial index sized for the sweep's hot path. Without it the daily
-- query scans the entire instances table on every tick. Mirrors the
-- watchdog's index (00016) — same shape, different state predicate.
create index if not exists instances_terminal_at_idx
  on instances (terminal_at)
  where state in ('stopped', 'failed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists instances_terminal_at_idx;
alter table instances drop column if exists terminal_at;
-- +goose StatementEnd