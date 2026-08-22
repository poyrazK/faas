-- 00350_reserve_slot.sql — temporary concurrent-PR migration fence.
-- The real migration for this slot is being coordinated by other
-- open PRs (PR #990 + PR #1017 own reservations in this range).
-- Remove this no-op when those migrations land, per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd