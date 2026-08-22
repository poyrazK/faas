-- filename: 00379_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-25 bridge fence: the round-24 renumber left slot 00379
-- without a real migration (the real E.2 backfill renumbered to
-- 00381). Pair-fence with 00378_reserve_slot.sql keeps the local
-- embed contiguous. SAFE-RELEASES Mega PR #1 (issue #976 / ADR-124)
-- — round-25 contiguity fill on 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
