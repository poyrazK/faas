-- filename: 20261001142049282_issue_assignee_list_index.sql

-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS app_issues_assignee_list_idx
    ON app_issues(app_id, assignee_account_id, last_seen_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS app_issues_assignee_list_idx;
-- +goose StatementEnd
