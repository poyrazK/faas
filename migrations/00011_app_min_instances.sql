-- +goose Up
-- +goose StatementBegin

-- M8 §6.5 cold-wake transparency: per-app floor that keeps N instances
-- resident even when idle, so the customer can opt out of the cold-wake
-- penalty via `faas app <slug> --min N` (Pro/Scale only). Default 0
-- preserves today's scale-to-zero behaviour for every existing app.
--
-- Idempotent (IF NOT EXISTS) so the migration is safe to re-run during
-- local development, matching 00010_account_deletion.sql style.

alter table apps
  add column if not exists min_instances int not null default 0;

-- Postgres does not accept `ADD CONSTRAINT IF NOT EXISTS` (only
-- ADD COLUMN / CREATE {TABLE,INDEX} accept the modifier). The drop +
-- add pair makes the constraint idempotent across re-runs while a
-- downstream migration is in flight, matching the style of 00010.
alter table apps drop constraint if exists apps_min_instances_check;
alter table apps
  add constraint apps_min_instances_check
  check (min_instances >= 0);

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin

alter table apps drop constraint if exists apps_min_instances_check;
alter table apps drop column if exists min_instances;

-- +goose StatementEnd