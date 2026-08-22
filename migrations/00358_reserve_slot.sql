-- 00358_reserve_slot.sql — temporary concurrent-PR migration fence.
-- PR #1005 api-contract-diff PR-A deployment_openapi_snapshots owns
-- 00358 as a real migration. Remove this no-op when that migration
-- lands, per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd