-- 00360_reserve_slot.sql — temporary concurrent-PR migration fence.
-- PR #1006 SAFE-RELEASES deployment_audit owns 00360 as a real
-- migration. Remove this no-op when that migration lands, per
-- ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd