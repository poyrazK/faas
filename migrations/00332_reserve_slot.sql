-- filename: 00332_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). PR #1005 owns slot 00330 as a real migration
-- deployment_openapi_snapshots + fences 00331-00333; until PR #1005
-- merges, the branch needs bridge reservations here so the local
-- chain stays gap-free. When PR #1005 merges, main's synthetic
-- merge will adopt those files and these local fences can be dropped
-- in a follow-up commit. SAFE-RELEASES Mega PR #1 (issue #976 /
-- ADR-122) — rebased onto main on 2026-08-20.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
