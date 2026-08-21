-- 00351_reserve_slot.sql — temporary concurrent-PR migration fence.
-- ADR-041 reservation. Stands in for a future migration claimed by an
-- in-flight PR (PR #1017 ADR-123 alert presets); lands on main only
-- when that PR merges. Removing this fence without filling the gap
-- will break TestMigrationsContiguous.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
