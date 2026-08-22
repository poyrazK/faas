-- 00366_reserve_slot.sql — temporary concurrent-PR migration fence.
-- PR #1017 meterd_tenant_surface_cert_expiry_state owns 00366 as
-- a real migration. Remove this no-op when that migration lands,
-- per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd