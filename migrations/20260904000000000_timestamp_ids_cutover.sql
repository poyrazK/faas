-- filename: 20260904000000000_timestamp_ids_cutover.sql
-- ADR-142: marks the end of the globally sequential migration namespace.
-- Future migrations use UTC timestamp IDs and may merge independently.

-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
