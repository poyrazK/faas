-- filename: 20261002212534134_service_capacity_projection_jit.sql
-- ADR-459: bound capacity projection latency without changing admission logic.
-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
    PERFORM singleton FROM service_capacity_policy WHERE singleton FOR UPDATE;
END; $$;
ALTER FUNCTION service_capacity_snapshot_inputs() SET jit = off;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    PERFORM singleton FROM service_capacity_policy WHERE singleton FOR UPDATE;
END; $$;
ALTER FUNCTION service_capacity_snapshot_inputs() RESET jit;
-- +goose StatementEnd
