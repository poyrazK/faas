-- +goose Up
-- A response-driven failure budget needs signed debt: requests already in
-- flight can fail after the last available token was spent. Keep that debt
-- separate from the nonnegative request-budget counter table.
CREATE TABLE pg_preauth_failure_counters (
    subject_id uuid NOT NULL,
    plan text NOT NULL CHECK (plan IN ('free', 'hobby', 'pro', 'scale')),
    tokens bigint NOT NULL,
    last_refill timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subject_id, plan)
);
CREATE INDEX pg_preauth_failure_counters_refill_idx
    ON pg_preauth_failure_counters (last_refill);

-- +goose Down
DROP TABLE IF EXISTS pg_preauth_failure_counters;
