-- filename: 00334_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). The SAFE-RELEASES Mega PR #1 foundation (issue #976 / ADR-122)
-- renumbered in round-11 to land at 00348+00349 (above main's fence
-- ceiling 00346 — main's 00346_deployments_annotation.sql real
-- migration that landed via PR #984 merging mid-round-10). Slots
-- 00334+00335 are vacated by the renumber and bridged with these
-- no-op fences so the local embed (which only sees this branch's
-- files) stays gap-free. When a future migration lands above 00349,
-- these fences can be dropped in a follow-up commit.
-- SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) — round-11 rebase
-- onto main on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
