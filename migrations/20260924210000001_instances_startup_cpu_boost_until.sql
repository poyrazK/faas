-- +goose Up
ALTER TABLE instances
    ADD COLUMN IF NOT EXISTS startup_cpu_boost_until timestamptz;

CREATE INDEX IF NOT EXISTS instances_startup_cpu_boost_until_idx
    ON instances (startup_cpu_boost_until)
    WHERE startup_cpu_boost_until IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS instances_startup_cpu_boost_until_idx;

ALTER TABLE instances
    DROP COLUMN IF EXISTS startup_cpu_boost_until;
