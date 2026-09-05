-- filename: 20260905000000001_runtime_config_rollouts.sql
-- ADR-132 follow-up: deterministic scoped/canary runtime configuration.
-- A rollout percentage is metadata on the durable override, not part of the
-- JSON value, so changing the target population is versioned and auditable.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE runtime_config_entries
    ADD COLUMN IF NOT EXISTS rollout_percent smallint NOT NULL DEFAULT 100;

ALTER TABLE runtime_config_revisions
    ADD COLUMN IF NOT EXISTS rollout_percent smallint NOT NULL DEFAULT 100;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'runtime_config_entries_rollout_percent_check'
    ) THEN
        ALTER TABLE runtime_config_entries
            ADD CONSTRAINT runtime_config_entries_rollout_percent_check
            CHECK (rollout_percent BETWEEN 0 AND 100);
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'runtime_config_revisions_rollout_percent_check'
    ) THEN
        ALTER TABLE runtime_config_revisions
            ADD CONSTRAINT runtime_config_revisions_rollout_percent_check
            CHECK (rollout_percent BETWEEN 0 AND 100);
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE runtime_config_revisions
    DROP CONSTRAINT IF EXISTS runtime_config_revisions_rollout_percent_check;
ALTER TABLE runtime_config_entries
    DROP CONSTRAINT IF EXISTS runtime_config_entries_rollout_percent_check;
ALTER TABLE runtime_config_revisions DROP COLUMN IF EXISTS rollout_percent;
ALTER TABLE runtime_config_entries DROP COLUMN IF EXISTS rollout_percent;
-- +goose StatementEnd
