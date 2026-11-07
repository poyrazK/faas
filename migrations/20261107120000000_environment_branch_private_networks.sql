-- filename: 20261107120000000_environment_branch_private_networks.sql
-- ADR-20261031193635 section 6: whether an environment branch's instances join
-- the private networks their apps are attached to. 'off' (the default, and every
-- existing branch) keeps them off; 'join' attaches them with an address of their
-- own, never the app's service address. Set at creation, never changed. Only apid
-- writes the column; vmmd reads it, and an older vmmd ignores it.
-- migration-compat: expand
--
-- The CHECK is added NOT VALID and validated in its own statement, so branches
-- keep being written during the scan. Every step is re-runnable.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE project_environment_branches ADD COLUMN IF NOT EXISTS private_networks text NOT NULL DEFAULT 'off';
-- +goose StatementBegin
DO $private_networks$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'project_environment_branches'::regclass AND conname = 'project_environment_branches_private_networks_chk') THEN
    ALTER TABLE project_environment_branches ADD CONSTRAINT project_environment_branches_private_networks_chk
      CHECK (private_networks IN ('off', 'join') AND (private_networks = 'off' OR egress_mode = 'live')) NOT VALID;
  END IF;
END
$private_networks$;
-- +goose StatementEnd
ALTER TABLE project_environment_branches VALIDATE CONSTRAINT project_environment_branches_private_networks_chk;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE project_environment_branches DROP CONSTRAINT IF EXISTS project_environment_branches_private_networks_chk;
ALTER TABLE project_environment_branches DROP COLUMN IF EXISTS private_networks;
