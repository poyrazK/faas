-- 00357_reserve_slot.sql — temporary concurrent-PR migration fence.
-- PR #990 ADR-117 PR-C app_secret_value_hash owns 00357 as a real
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