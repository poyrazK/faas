-- filename: 00334_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). The SAFE-RELEASES Mega PR #1 foundation (issue #976 / ADR-122)
-- renumbered in round-14 to land at 00354+00355 (above PR #1012's
-- renumber to 00352_deployments_stage_state_history_cap.sql — PR #1012
-- re-fenced its 00347-00351 range and bumped its real migration to
-- 00352 while round-13 was in flight). Slots 00334+00335 are
-- vacated by the renumber and bridged with these no-op fences so the
-- local embed (which only sees this branch's files) stays gap-free.
-- When a future migration lands above 00355, these fences can be
-- dropped in a follow-up commit.
-- SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) — round-14 rebase
-- onto main on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
