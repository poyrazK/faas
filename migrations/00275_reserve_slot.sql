-- filename: 00275_reserve_slot.sql
-- +goose Up
-- +goose StatementBegin
--
-- 00275_reserve_slot.sql — reservation fence.
--
-- This slot is claimed by open PR #910 (feat(triggers): unified
-- event-source-mapping primitive — issue #757 / ADR-100), which
-- introduces migrations/00275_triggers_poison_strategy.sql. See
-- the 00273_reserve_slot.sql fence header for the cross-PR slot
-- precheck pattern. This file is a no-op; the actual migration
-- lands when PR #910 merges.

select 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

select 1;

-- +goose StatementEnd
