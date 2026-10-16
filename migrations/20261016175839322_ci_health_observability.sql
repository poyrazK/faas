-- +goose Up
-- Fixed diagnostic slots, never a work/admission lease. No host or account labels.
CREATE TABLE IF NOT EXISTS ci_worker_health (
 worker text NOT NULL CHECK(worker IN ('dispatcher','metadata_admission','automation_admission','metadata_checks','execution_checks','retention')),
 slot integer NOT NULL CHECK(slot BETWEEN 0 AND 7),
 token uuid NOT NULL,
 enabled boolean NOT NULL,
 ready boolean NOT NULL,
 started_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 cycle_at timestamptz,
 cycle_sequence bigint NOT NULL DEFAULT 0 CHECK(cycle_sequence>=0),
 success_at timestamptz,
 last_error text NOT NULL DEFAULT '' CHECK(last_error IN ('','unavailable')),
 PRIMARY KEY(worker,slot),
 CHECK(success_at IS NULL OR cycle_at IS NOT NULL)
);

-- +goose Down
DROP TABLE ci_worker_health;
