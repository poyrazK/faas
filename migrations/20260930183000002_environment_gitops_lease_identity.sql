-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS environment_gitops_runs_source_lease_uniq
    ON environment_gitops_runs(source_id, lease_token);

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
