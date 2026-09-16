-- +goose Up
-- +goose StatementBegin

-- Issue #2415: every cold wake creates a fresh instances row and parking it
-- stamps parked_at, while the reusable restore artifact remains in snapshots.
-- The hourly schedd retention pass can therefore reclaim old PARKED history.
-- Keep the index predicate identical to PgStore's eligibility predicate so
-- the oldest-first bounded delete is an index scan rather than a table scan.
CREATE INDEX IF NOT EXISTS instances_parked_at_retention_idx
    ON instances (parked_at, id)
    WHERE app_id IS NOT NULL
      AND state = 'parked'
      AND lease_token IS NULL
      AND migration_started_at IS NULL
      AND parked_at IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS instances_parked_at_retention_idx;

-- +goose StatementEnd
