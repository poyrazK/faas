-- filename: 00593_reserve_slot.sql
-- +goose Up
-- +goose StatementBegin
-- ADR-041 cross-PR slot reservation fence for workflow mega-PR cluster headroom.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
