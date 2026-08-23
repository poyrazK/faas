-- 00380_reserve_slot.sql — temporary concurrent-PR migration fence.
-- The real migration for this slot is being coordinated by other
-- open PRs (PR #1006 deployment_audit). Remove this no-op when
-- those migrations land, per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd