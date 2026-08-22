-- filename: 00362_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-20 bridge fence: PR #1024 (ADR-124 deployment queue
-- controls) owns 00362_deployments_cancelled.sql in its branch but
-- the file is not in this branch's local embed. The real E.2
-- migrations renumbered in round-20 to 00364+00365. SAFE-RELEASES
-- Mega PR #1 (issue #976 / ADR-124) — round-20 contiguity fill on
-- 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
