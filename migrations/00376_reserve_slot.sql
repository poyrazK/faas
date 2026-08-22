-- filename: 00376_reserve_slot.sql
-- +goose Up
-- +goose StatementBegin

-- 00376_reserve_slot.sql — fence dragged forward from
-- origin/main as part of PR #1030 (ADR-123 members_only)
-- slot dance. origin/main had a fence at slot 00376
-- (placed by Mega-C / issue #961 + successor PRs); the
-- embed-test `TestMigrationsUniquePrefixes` requires the
-- [1..N] prefix be contiguous on every branch, so we
-- mirror origin/main's reservation here. The fence body
-- is a no-op `SELECT 1;` (matches origin/main's shape).

SELECT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SELECT 1;

-- +goose StatementEnd
