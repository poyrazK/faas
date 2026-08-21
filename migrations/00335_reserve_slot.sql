-- filename: 00335_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). The SAFE-RELEASES Mega PR #1 foundation (issue #976 / ADR-122)
-- renumbered in round-13 to land at 00352+00353 (above PR #1017's
-- slot range 00347-00351 — PR #1017's ADR-123 alert presets shipped
-- 5 real migrations at slots 00347-00351 while round-12 was in
-- flight, blocking the renumber there). Slots 00334+00335 are
-- vacated by the renumber and bridged with these no-op fences so the
-- local embed (which only sees this branch's files) stays gap-free.
-- When a future migration lands above 00353, these fences can be
-- dropped in a follow-up commit.
-- SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) — round-13 rebase
-- onto main on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
