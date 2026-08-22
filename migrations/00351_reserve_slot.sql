-- filename: 00351_reserve_slot.sql
-- +goose Up
-- +goose StatementBegin

-- 00351_reserve_slot.sql — reservation fence.
--
-- This slot is fenced by Mega-C (issue #961, PR-1 + PR-2). The
-- Mega-C work needs slots 00353 (PR-1 preview_destroy_commented_at)
-- and 00354 (PR-2 deployments_rollback_on_5xx). Slots 00347-00352
-- are fenced here with distinct comment blocks pointing to issue
-- #961 so the cross-PR slot gate (memory:
-- cross-pr-slot-gate-reservation-fence-pattern) and the fence-
-- deletion hazard (memory: cross-pr-rebase-fence-deletion-hazard)
-- do not delete the wrong copy on merge. PR #1036 also fences 00351
-- on its own branch; the merge-order gate handles the dedupe.

SELECT 1;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

SELECT 1;

-- +goose StatementEnd