-- 00374_reserve_slot.sql — temporary concurrent-PR migration fence.
-- Slots 00373-00374 are skipped by this branch so the two real
-- migrations (PR-4 vmmd cert fingerprint guard, PR-5 cluster
-- wakeCoord) can land at 00376 + 00377 (past main's
-- 00375_endpoint_discovery.sql). The placeholder rows reserve
-- the slot numbers in the per-test schema. Remove when the
-- surrounding real migrations land, per ADR-041.
-- +goose Up
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd