-- filename: 00371_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-22 bridge fence: PR #1017 (ADR-123 alert-presets) claims
-- this slot for 00371_account_spend_snapshot.sql but the file is
-- not in this branch's local embed. The real E.2 migrations
-- renumbered in round-22 from 00370+00371 to 00373+00374 to clear
-- PR #1017's round-22 expansion. SAFE-RELEASES Mega PR #1 (issue #976
-- / ADR-124) — round-22 contiguity fill on 2026-08-22.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
