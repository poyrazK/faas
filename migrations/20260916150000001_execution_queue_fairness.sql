-- filename: 20260916150000001_execution_queue_fairness.sql

-- +goose Up
-- +goose StatementBegin
-- The fair dispatcher groups queued work by account before selecting a
-- claimant. Keep that lookup on a narrow partial index so bursts of
-- disposable runs do not scan terminal history or active leases.
CREATE INDEX IF NOT EXISTS executions_queue_account_idx
    ON executions (account_id, created_at, id)
    WHERE status = 'queued' AND cancel_requested_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS executions_queue_account_idx;
-- +goose StatementEnd
