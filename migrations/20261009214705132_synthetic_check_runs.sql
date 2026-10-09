-- +goose Up
-- ADR-748 slice 2: one row per synthetic check run, written by meterd and
-- kept SyntheticCheckRunRetentionDays (7). error_class is a bounded
-- vocabulary so a hostile response cannot write arbitrary text.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS synthetic_check_runs (
    check_id uuid NOT NULL REFERENCES synthetic_checks(id) ON DELETE CASCADE,
    started_at timestamp with time zone NOT NULL,
    ok boolean NOT NULL,
    status_code integer NOT NULL,
    latency_ms integer NOT NULL,
    error_class text NOT NULL,
    CONSTRAINT synthetic_check_runs_pkey PRIMARY KEY (check_id, started_at),
    CONSTRAINT synthetic_check_runs_status_chk CHECK (status_code = 0 OR status_code BETWEEN 100 AND 599),
    CONSTRAINT synthetic_check_runs_latency_chk CHECK (latency_ms >= 0),
    CONSTRAINT synthetic_check_runs_error_chk CHECK (error_class IN ('', 'status', 'timeout', 'dns', 'connect', 'tls', 'other')),
    CONSTRAINT synthetic_check_runs_ok_chk CHECK (ok = (error_class = ''))
);
CREATE INDEX IF NOT EXISTS synthetic_check_runs_started_idx ON synthetic_check_runs (started_at);
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS synthetic_check_runs;
