-- filename: 00378_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-25 bridge fence: PR #1030 (members_only) claims this slot
-- for 00378_apps_public_auth_members_only.sql but the file is not in
-- this branch's local embed. The real E.2 migrations renumbered in
-- round-25 from 00378+00379 to 00380+00381 to clear PR #1030's
-- pre-claim that the cross-PR slot precheck caught. SAFE-RELEASES
-- Mega PR #1 (issue #976 / ADR-124) — round-25 contiguity fill on
-- 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
