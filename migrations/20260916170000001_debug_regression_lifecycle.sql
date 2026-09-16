-- +goose Up
-- Debugger regression workflow state is durable so detector refreshes do not
-- erase an operator acknowledgement or dismissal.
-- +goose StatementBegin
ALTER TABLE debug_regression_observations
    ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'active',
    ADD COLUMN IF NOT EXISTS acknowledged_at timestamptz,
    ADD COLUMN IF NOT EXISTS dismissed_until timestamptz,
    ADD COLUMN IF NOT EXISTS resolved_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
         WHERE conname = 'debug_regression_observations_state_check'
           AND conrelid = 'debug_regression_observations'::regclass
    ) THEN
        ALTER TABLE debug_regression_observations
            ADD CONSTRAINT debug_regression_observations_state_check
            CHECK (state IN ('active', 'acknowledged', 'dismissed', 'resolved'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS debug_regression_observations_lifecycle_idx
    ON debug_regression_observations (state, dismissed_until, last_detected_at DESC);
-- +goose StatementEnd

-- +goose Down
-- Sentinel: lifecycle state is part of the persisted debugger contract and
-- is intentionally not removed by an automatic down migration.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
