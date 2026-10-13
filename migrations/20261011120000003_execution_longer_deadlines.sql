-- filename: 20261011120000003_execution_longer_deadlines.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-987: Run deadlines up to 15 minutes (api.ExecutionTimeoutHardMaxMS).
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_timeout_ms_check;
ALTER TABLE executions ADD CONSTRAINT executions_timeout_ms_check
    CHECK (timeout_ms BETWEEN 100 AND 900000);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_timeout_ms_check;
ALTER TABLE executions ADD CONSTRAINT executions_timeout_ms_check
    CHECK (timeout_ms BETWEEN 100 AND 30000) NOT VALID;
-- +goose StatementEnd
