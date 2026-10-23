-- filename: 20261023084251283_vmmd_ownership_epoch.sql
-- migration-compat: expand
-- vmmd restart adoption ADR §3, slice 3: the node's ownership epoch. Each
-- vmmd start claims the next epoch with a compare-and-swap; adoption
-- reports and capacity telemetry carry it, and schedd ignores an epoch
-- below the row's. vmmd_epoch_incarnation is the claimant, and
-- vmmd_predecessor_incarnation the incarnation whose guests it may adopt.
-- vmmd_restart_notice_until is set by node_join before a planned restart.
-- Expand-only: binaries without slice 3 ignore the columns.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Columns bare, then the checks NOT VALID (no-downtime rules): new writes
-- are checked at once, and existing rows hold the defaults, which pass.
ALTER TABLE compute_nodes
 ADD COLUMN IF NOT EXISTS vmmd_epoch bigint NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS vmmd_epoch_incarnation uuid,
 ADD COLUMN IF NOT EXISTS vmmd_predecessor_incarnation uuid,
 ADD COLUMN IF NOT EXISTS vmmd_restart_notice_until timestamptz;
-- Guarded so the migration replays over its own effects (replay_safety_test.go).
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'compute_nodes'::regclass AND conname = 'compute_nodes_vmmd_epoch_check') THEN
  ALTER TABLE compute_nodes ADD CONSTRAINT compute_nodes_vmmd_epoch_check
   CHECK (vmmd_epoch >= 0) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'compute_nodes'::regclass AND conname = 'compute_nodes_vmmd_epoch_incarnation_check') THEN
  ALTER TABLE compute_nodes ADD CONSTRAINT compute_nodes_vmmd_epoch_incarnation_check
   CHECK (vmmd_epoch_incarnation <> '00000000-0000-0000-0000-000000000000'::uuid) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'compute_nodes'::regclass AND conname = 'compute_nodes_vmmd_predecessor_incarnation_check') THEN
  ALTER TABLE compute_nodes ADD CONSTRAINT compute_nodes_vmmd_predecessor_incarnation_check
   CHECK (vmmd_predecessor_incarnation <> '00000000-0000-0000-0000-000000000000'::uuid) NOT VALID;
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'compute_nodes'::regclass AND conname = 'compute_nodes_vmmd_epoch_claimant_check') THEN
  ALTER TABLE compute_nodes ADD CONSTRAINT compute_nodes_vmmd_epoch_claimant_check
   CHECK ((vmmd_epoch = 0) = (vmmd_epoch_incarnation IS NULL)) NOT VALID;
 END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE compute_nodes DROP CONSTRAINT IF EXISTS compute_nodes_vmmd_epoch_claimant_check;
ALTER TABLE compute_nodes
 DROP COLUMN IF EXISTS vmmd_restart_notice_until,
 DROP COLUMN IF EXISTS vmmd_predecessor_incarnation,
 DROP COLUMN IF EXISTS vmmd_epoch_incarnation,
 DROP COLUMN IF EXISTS vmmd_epoch;
-- +goose StatementEnd
