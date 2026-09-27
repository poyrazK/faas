-- filename: 20260926090059069_workflow_step_attempts.sql

-- +goose Up
-- +goose StatementBegin

-- Keep one durable history row per executor invocation. The parent step row
-- remains the compact current-state summary exposed by the existing endpoint.
CREATE TABLE IF NOT EXISTS workflow_step_attempts (
    run_id          uuid NOT NULL,
    step_name       text NOT NULL,
    attempt         integer NOT NULL CHECK (attempt > 0),
    status          text NOT NULL
                    CHECK (status IN ('running','retrying','succeeded','failed')),
    http_status     integer CHECK (http_status BETWEEN 100 AND 599),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    next_attempt_at timestamptz,
    error           text,
    PRIMARY KEY (run_id, step_name, attempt),
    FOREIGN KEY (run_id, step_name)
        REFERENCES workflow_steps (run_id, step_name) ON DELETE CASCADE
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS workflow_step_attempts;
-- +goose StatementEnd
