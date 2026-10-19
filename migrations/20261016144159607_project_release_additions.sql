-- migration-compat: expand
-- filename: 20261016144159607_project_release_additions.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS project_release_additions (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    attempt_id text NOT NULL CHECK (attempt_id ~ '^[a-f0-9]{32}$'),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    dormant boolean NOT NULL DEFAULT true,
    FOREIGN KEY (account_id, attempt_id) REFERENCES project_submissions(account_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS project_release_additions_project_idx ON project_release_additions(project_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE project_release_additions;
-- +goose StatementEnd
