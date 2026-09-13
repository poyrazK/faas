-- filename: 20260913100000000_execution_usage_ledger.sql

-- +goose Up
-- +goose StatementBegin

-- Durable, payload-independent accounting facts for disposable executions.
-- One row per execution is the idempotency boundary: scheduler retries,
-- lease recovery, and duplicate terminalization attempts cannot charge twice.
CREATE TABLE IF NOT EXISTS execution_usage_ledger (
    execution_id   uuid PRIMARY KEY REFERENCES executions(id) ON DELETE CASCADE,
    account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    runtime        text NOT NULL,
    status         text NOT NULL,
    wall_time_ms   bigint NOT NULL DEFAULT 0,
    cpu_time_ms    bigint NOT NULL DEFAULT 0,
    peak_memory_mb bigint NOT NULL DEFAULT 0,
    output_bytes   bigint NOT NULL DEFAULT 0,
    started_at     timestamptz,
    finished_at    timestamptz NOT NULL,
    created_at     timestamptz NOT NULL,
    recorded_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT execution_usage_ledger_runtime_check CHECK (
        runtime IN ('node22', 'node24', 'python312', 'python313')
    ),
    CONSTRAINT execution_usage_ledger_status_check CHECK (
        status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
    ),
    CONSTRAINT execution_usage_ledger_nonnegative_check CHECK (
        wall_time_ms >= 0 AND cpu_time_ms >= 0 AND peak_memory_mb >= 0 AND output_bytes >= 0
    ),
    CONSTRAINT execution_usage_ledger_finished_check CHECK (finished_at >= created_at),
    CONSTRAINT execution_usage_ledger_started_check CHECK (
        started_at IS NULL OR (started_at >= created_at AND finished_at >= started_at)
    )
);

CREATE INDEX IF NOT EXISTS execution_usage_ledger_account_finished_idx
    ON execution_usage_ledger (account_id, finished_at DESC, execution_id DESC);

-- Backfill terminal rows created before this migration. The insert is
-- idempotent so a restored database can safely replay migration setup.
INSERT INTO execution_usage_ledger (
    execution_id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
    peak_memory_mb, output_bytes, started_at, finished_at, created_at
)
SELECT id, account_id, runtime, status, wall_time_ms, cpu_time_ms,
       peak_memory_mb,
       (result_bytes + octet_length(stdout) + octet_length(stderr))::bigint,
       started_at, finished_at, created_at
FROM executions
WHERE status IN ('succeeded', 'failed', 'timed_out', 'out_of_memory', 'cancelled')
ON CONFLICT (execution_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS execution_usage_ledger_account_finished_idx;
DROP TABLE IF EXISTS execution_usage_ledger;
-- +goose StatementEnd
