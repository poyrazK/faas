-- migration-compat: expand
-- Superseded before it ran in production. This single-transaction cutover
-- deadlocked against live writers during the rc.277 rollout, and no
-- production database applied it. 20261019190114207..676 apply the same
-- organization ownership schema online (ADR-202610171908). The version stays
-- so goose's ledger order is unchanged.
-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
