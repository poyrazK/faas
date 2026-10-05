-- +goose Up
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_definitions_route_idx ON customer_operation_definitions
 (deployment_id,(spec->>'method'),(spec->>'path'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
