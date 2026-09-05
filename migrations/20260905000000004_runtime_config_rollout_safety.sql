-- filename: 20260905000000004_runtime_config_rollout_safety.sql
-- Runtime-config rollout lifecycle. The state is separate from the applied
-- status so an effective canary can be paused or recorded as rolled back
-- without making daemons fall back to an older value accidentally.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE runtime_config_entries
    ADD COLUMN IF NOT EXISTS rollout_state text NOT NULL DEFAULT 'stable';

UPDATE runtime_config_entries
SET rollout_state = CASE
    WHEN rollout_percent > 0 AND rollout_percent < 100 THEN 'canary'
    ELSE 'stable'
END
WHERE rollout_state = 'stable';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'runtime_config_entries_rollout_state_check'
    ) THEN
        ALTER TABLE runtime_config_entries
            ADD CONSTRAINT runtime_config_entries_rollout_state_check
            CHECK (rollout_state IN ('stable', 'canary', 'promoting', 'paused', 'rolled_back'));
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE runtime_config_entries
    DROP CONSTRAINT IF EXISTS runtime_config_entries_rollout_state_check;
ALTER TABLE runtime_config_entries
    DROP COLUMN IF EXISTS rollout_state;
-- +goose StatementEnd
