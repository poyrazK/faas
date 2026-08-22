-- 00359_reserve_slot.sql — temporary concurrent-PR migration fence.
-- (No sibling-PR real migration at this slot at present; held in
-- reserve for merge-order coordination per ADR-041.)
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd