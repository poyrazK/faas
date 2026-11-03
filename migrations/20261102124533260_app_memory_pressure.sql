-- filename: 20261102124533260_app_memory_pressure.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks apps briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261102124306: schedd keeps one memory-pressure rollup per app from
-- the guests' reports, so app health can warn before an OOM kill. The window
-- starts at the first report and lasts at most 24 hours; a report after that
-- starts a new one. A new table, so older schedulers keep working.
CREATE TABLE IF NOT EXISTS app_memory_pressure (
    app_id              uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    window_started_at   timestamptz NOT NULL CHECK (isfinite(window_started_at)),
    episodes            integer NOT NULL CHECK (episodes > 0),
    lowest_available_mb integer NOT NULL CHECK (lowest_available_mb >= 0),
    total_mb            integer NOT NULL CHECK (total_mb >= 0),
    peak_anon_mb        integer NOT NULL CHECK (peak_anon_mb >= 0),
    last_at             timestamptz NOT NULL CHECK (isfinite(last_at)),
    CHECK (last_at >= window_started_at)
);
COMMENT ON TABLE app_memory_pressure IS
    'ADR-20261102124306 per-app memory-pressure rollup written by schedd from guest reports.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app_memory_pressure;
-- +goose StatementEnd
