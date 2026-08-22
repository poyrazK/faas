-- 00368_reserve_slot.sql — temporary concurrent-PR migration fence.
-- Slots 00368-00372 are claimed by open PR #1017 (ADR-123 alert
-- preset catalog) on branch worktree-alert-presets-adr-123. The real
-- migration on this branch (PR-4 vmmd cert fingerprint guard) lands
-- at slot 00373; this fence absorbs the renumber hop until PR #1017
-- merges. Remove this no-op when the surrounding real migrations
-- land, per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd