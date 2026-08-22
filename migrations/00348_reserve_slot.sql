-- 00348_reserve_slot.sql — multi-host safety cluster PR-4 fence.
-- The real migration for this slot is being coordinated by other
-- open PRs. Remove this no-op when those migrations land, per
-- ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 'reserved 00348 — multi-host safety cluster PR-4 fence' AS notice;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'released 00348' AS notice;
-- +goose StatementEnd