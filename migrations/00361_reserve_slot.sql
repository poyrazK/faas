-- filename: 00361_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-19 bridge fence: PR #1023 (ADR-124 per-service
-- wire-protocol selector) may extend into 00361 from its current
-- 00360 real-migration claim. The real E.2 migrations renumbered in
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
