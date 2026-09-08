-- Optional per-cron scheduling controls. Legacy rows default to UTC and
-- continue to dispatch on every boundary unless overlap skipping is enabled.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons
    ADD COLUMN IF NOT EXISTS timezone text NOT NULL DEFAULT 'UTC';

ALTER TABLE crons
    ADD COLUMN IF NOT EXISTS skip_if_running boolean NOT NULL DEFAULT false;

ALTER TABLE crons
    DROP CONSTRAINT IF EXISTS crons_timezone_nonempty_check;

ALTER TABLE crons
    ADD CONSTRAINT crons_timezone_nonempty_check CHECK (btrim(timezone) <> '');
-- +goose StatementEnd

-- +goose Down
-- Keep the columns on rollback: newer schedulers may have persisted values,
-- and dropping them would make a downgrade destructive to customer intent.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
