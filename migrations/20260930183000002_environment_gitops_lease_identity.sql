-- +goose Up
CREATE UNIQUE INDEX environment_gitops_runs_source_lease_uniq
    ON environment_gitops_runs(source_id, lease_token);

-- +goose Down
DROP INDEX environment_gitops_runs_source_lease_uniq;
