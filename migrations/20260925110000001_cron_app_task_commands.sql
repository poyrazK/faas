-- Extend the existing cron resource with deployment-attached command runs.
-- The schedule remains managed by `gregale crons`; HTTP path crons and app
-- command crons share one lifecycle and quota surface.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons
    ADD COLUMN IF NOT EXISTS command text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS command_shell boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS command_timeout_seconds integer NOT NULL DEFAULT 600,
    ADD COLUMN IF NOT EXISTS command_max_output_bytes integer NOT NULL DEFAULT 1048576;

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_command_shape_check;
ALTER TABLE crons ADD CONSTRAINT crons_command_shape_check CHECK (
    (cardinality(command) = 0 AND NOT command_shell)
    OR
    (cardinality(command) BETWEEN 1 AND 64
     AND array_position(command, NULL) IS NULL
     AND octet_length(command[1]) BETWEEN 1 AND 4096
     AND octet_length(array_to_string(command, '')) BETWEEN 1 AND 16384
     AND (NOT command_shell OR cardinality(command) = 1))
);

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_command_timeout_check;
ALTER TABLE crons ADD CONSTRAINT crons_command_timeout_check
    CHECK (command_timeout_seconds BETWEEN 1 AND 3600);

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_command_output_check;
ALTER TABLE crons ADD CONSTRAINT crons_command_output_check
    CHECK (command_max_output_bytes BETWEEN 1024 AND 16777216);

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_app_schedule_path_unique;
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_app_schedule_path_command_unique;
ALTER TABLE crons ADD CONSTRAINT crons_app_schedule_path_command_unique
    UNIQUE (app_id, schedule, path, command);

ALTER TABLE app_tasks
    ADD COLUMN IF NOT EXISTS cron_id uuid REFERENCES crons(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS scheduled_for timestamptz;

ALTER TABLE app_tasks DROP CONSTRAINT IF EXISTS app_tasks_kind_chk;
ALTER TABLE app_tasks ADD CONSTRAINT app_tasks_kind_chk
    CHECK (kind IN ('manual', 'release', 'cron'));

CREATE INDEX IF NOT EXISTS app_tasks_cron_runs_idx
    ON app_tasks (cron_id, created_at DESC, id DESC)
    WHERE cron_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS app_tasks_cron_schedule_fire_unique
    ON app_tasks (cron_id, scheduled_for)
    WHERE cron_id IS NOT NULL AND scheduled_for IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Keep the added columns and widened CHECK in place during rollback: command
-- schedules and their task history are customer intent, so a code rollback
-- must not destroy or invalidate that data.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
