-- filename: 00359_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-18 bridge fence: PR #1012's stage retries / SLO
-- histograms (proposed follow-on) may claim slot 00359; PR #1024 may
-- also extend into 00359. This is a placeholder until those slots
-- resolve. SAFE-RELEASES Mega PR #1 (issue #976 / ADR-122) —
-- round-18 contiguity fill on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
