-- +goose Up
-- +goose StatementBegin
-- Retained operations restrict definition removal. Once operations expire,
-- retiring a deployment also removes its obsolete immutable definitions.
ALTER TABLE customer_operation_definitions DROP CONSTRAINT IF EXISTS customer_operation_definitions_deployment_id_fkey;
ALTER TABLE customer_operation_definitions ADD CONSTRAINT customer_operation_definitions_deployment_id_fkey
 FOREIGN KEY(deployment_id) REFERENCES deployments(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
