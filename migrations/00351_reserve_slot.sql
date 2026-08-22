-- 00351_reserve_slot.sql — multi-host safety cluster PR-5 fence.
-- The real migration for this slot is being coordinated by other
-- open PRs. Remove this no-op when those migrations land, per
-- ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 'reserved 00351 — multi-host safety cluster PR-5 fence' AS notice;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'released 00351' AS notice;
-- +goose StatementEnd