-- filename: 00366_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-21 bridge fence: PR #1017 (ADR-123 alert-presets) fences
-- 00366 in its branch but the file is not in this branch's local
-- embed. The real E.2 migrations renumbered in round-21 to
-- 00370+00371. SAFE-RELEASES Mega PR #1 (issue #976 / ADR-124) —
-- round-21 contiguity fill on 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
