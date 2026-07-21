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

-- Backfill existing terminal rows from the best available clock so
-- the sweep ages them correctly. The fallback chain is:
--   1. parked_at — schema populated this on every PARKED transition
--      (migration 00003 / 00015); a STOPPED row that was previously
--      parked carries the timestamp of the last park, which is a
--      reasonable lower bound for "how long ago this row entered a
--      terminal-ish state".
--   2. started_at — created on INSERT (migration 00015). For rows
--      that never parked first (rare; pre-§6.1 watchdog), this is
--      the row age itself, which is the worst-case but correct
--      upper bound.
--   3. now() - interval '30 days' — pure-safety floor for any legacy
--      rows that somehow have neither clock (one-box only has these
--      on rows from the v1 schema pre-00015, which never completed
--      migration 00015). Anchoring them 30d in the past gives the
--      full retention window before the first sweep can delete them;
--      a tighter fallback (e.g. now()) would silently drop them on
--      the very first retention tick after deploy.
update instances
  set terminal_at = coalesce(parked_at, started_at, now() - interval '30 days')
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
