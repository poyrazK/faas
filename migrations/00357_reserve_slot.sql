-- filename: 00357_reserve_slot.sql
-- Reserved slot to keep the migrations directory contiguous for the
-- local embed (CI's TestMigrationsContiguous pins slot N to position
-- N). Round-18 bridge fence: PR #1005 claims
-- 00357_deployment_openapi_snapshots.sql and PR #990 claims
-- 00357_app_secret_value_hash.sql as their real migrations (only one
-- will merge). Once either PR merges, this fence can be dropped in
-- a follow-up commit. SAFE-RELEASES Mega PR #1 (issue #976 /
-- ADR-122) — round-18 contiguity fill on 2026-08-21.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
