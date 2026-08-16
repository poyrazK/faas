-- filename: 00273_reserve_slot.sql
-- +goose Up
-- +goose StatementBegin
--
-- 00273_reserve_slot.sql — reservation fence.
--
-- This slot is claimed by open PR #910 (feat(triggers): unified
-- event-source-mapping primitive — issue #757 / ADR-100) which
-- introduces migrations/00273_triggers.sql + 00274 +
-- 00275_triggers_poison_strategy.sql. The fence prevents
-- goose "duplicate version 273" collision if PR #910 merges
-- first or second relative to PR #936 (issue #911 multi-host
-- scale-out gaps #3-#6).
--
-- This file is a no-op. The next migration PR to claim 00273
-- will collide with this fence via TestMigrationsContiguous —
-- the cross-PR slot precheck (memory/cross-pr-slot-precheck-
-- pr-867-collision-2026-08-13.md) gates this at PR-open time.
--
-- Once PR #910 lands, this fence will be removed by a follow-up
-- rebase commit. See memory/cross-pr-slot-fence-pagination-gate.md
-- for the broader pattern.

select 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

select 1;

-- +goose StatementEnd
