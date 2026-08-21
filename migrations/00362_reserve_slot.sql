-- filename: 00362_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-19 bridge fence: PR #1017 (ADR-123 alert-presets)
-- claims this slot for 00362_alert_presets.sql but the file is not in
-- this branch's local embed. The real E.2 migrations renumbered in
-- round-19 to 00363+00364. SAFE-RELEASES Mega PR #1 (issue #976 /
-- ADR-124) — round-19 contiguity fill on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
