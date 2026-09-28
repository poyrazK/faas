-- +goose Up
-- +goose StatementBegin
-- Newly claimed runs recover after five minutes if the scheduler dies before
-- invoking a step. An executing step extends the lease by its declared
-- timeout plus five minutes, preserving the no-overlap safety margin.
ALTER TABLE workflow_runs ADD COLUMN IF NOT EXISTS lease_until timestamptz;
CREATE INDEX IF NOT EXISTS workflow_runs_running_lease_idx ON workflow_runs (lease_until, id)
    WHERE status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS workflow_runs_running_lease_idx;
ALTER TABLE workflow_runs DROP COLUMN IF EXISTS lease_until;
-- +goose StatementEnd
