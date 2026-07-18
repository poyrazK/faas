-- +goose Up
-- +goose StatementBegin

-- Partial index for schedd's idle reaper (spec §17 G7). pkg/state.pgstore's
-- ListAllInstances runs once per reaper tick (every 10 s) with a state filter
-- on the four live states — the index must keep that query off the full table
-- as parked/stopped/failed instances accumulate over the life of the box.
--
-- Leading on (started_at desc) so the planner can also satisfy ORDER BY
-- started_at DESC without a sort step. Partial so parked rows (the majority
-- after warm-up) don't bloat the index.

create index if not exists instances_reaper_state_idx
  on instances (started_at desc)
  where state in ('running','waking','cold_booting','snapshotting');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists instances_reaper_state_idx;
-- +goose StatementEnd
