-- filename: 00354_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-18 bridge fence: PR #1024 (ADR-124 queue controls) claims
-- 00354_builds_cancelled.sql as its real migration. Once PR #1024
-- merges, this fence can be dropped in a follow-up commit.
-- SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) — round-18
-- contiguity fill on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
