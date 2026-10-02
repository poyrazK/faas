-- filename: 20261002162554006_issue_ownership_rules.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_issue_ownership_rules (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    rule_order integer NOT NULL CHECK (rule_order BETWEEN 0 AND 49),
    exception_type text,
    source_kind text CHECK (source_kind IS NULL OR source_kind IN ('exception','http','runtime','worker')),
    route_prefix text CHECK (route_prefix IS NULL OR (length(route_prefix) <= 256 AND left(route_prefix, 1) = '/' AND position('?' in route_prefix) = 0 AND position('#' in route_prefix) = 0)),
    assignee_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    PRIMARY KEY (app_id, rule_order),
    CHECK ((exception_type IS NOT NULL AND length(exception_type) BETWEEN 1 AND 256)
        OR source_kind IS NOT NULL
        OR route_prefix IS NOT NULL)
);
-- +goose StatementEnd

-- +goose Down
-- Ownership decisions already written to issue history are durable. Keep
-- configured rules across rollback and recreate the table if the feature is
-- restored, following the issue-history rollback policy.
SELECT 1;
-- +goose StatementEnd
