-- filename: 00335_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). The SAFE-RELEASES Mega PR #1 foundation (issue #976 / ADR-122)
-- renumbered in round-9 to land at 00342+00343 (above main's fence
-- ceiling 00339 and main's 00341_repair_app_secrets_scope.sql real
-- migration). Slots 00334+00335 are vacated by the renumber and
-- bridged with these no-op fences so the local embed (which only sees
-- this branch's files) stays gap-free. When a future migration lands
-- above 00343, these fences can be dropped in a follow-up commit.
-- SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) — round-9 rebase
-- onto main on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
