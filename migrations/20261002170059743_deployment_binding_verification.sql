-- filename: 20261002170059743_deployment_binding_verification.sql

-- +goose Up
-- +goose StatementBegin
CREATE INDEX app_tasks_binding_verification_deployment_idx ON app_tasks
 (account_id, app_id, deployment_id, (binding_verification->>'type'), (binding_verification->>'binding'), deployment_scope, created_at DESC, id DESC)
 WHERE binding_verification IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX app_tasks_binding_verification_deployment_idx;
-- +goose StatementEnd
