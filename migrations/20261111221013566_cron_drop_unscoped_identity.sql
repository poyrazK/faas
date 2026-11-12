-- filename: 20261111221013566_cron_drop_unscoped_identity.sql
-- migration-compat: contract
-- ADR-20261104120000: the app-wide cron identity would refuse a stage cron
-- with production's schedule, path and command. The per-environment unique
-- index (20261111221012012_cron_scope_indexes) carries the identity now.
-- Contract: only a full release drops it. Until then a stage cron that
-- repeats a production cron's identity is refused as a conflict.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_app_schedule_path_command_unique;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DELETE FROM crons WHERE deployment_scope<>'';
ALTER TABLE crons ADD CONSTRAINT crons_app_schedule_path_command_unique UNIQUE (app_id, schedule, path, command);
-- +goose StatementEnd
