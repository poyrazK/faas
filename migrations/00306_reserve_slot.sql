-- filename: 00306_reserve_slot.sql
-- ADR-041 cross-PR slot fence: this slot is reserved for a future PR
-- and intentionally applies no DDL. Do NOT land schema here until the
-- owning ADR is merged — the slot exists to keep the embedded
-- migration set contiguous so TestMigrationsContiguous stays green.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
