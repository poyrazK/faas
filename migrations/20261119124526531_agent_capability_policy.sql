-- filename: 20261119124526531_agent_capability_policy.sql
-- migration-compat: expand

-- +goose Up
-- The column and the constraint lock agent_versions briefly; a NULL column
-- rewrites no rows and a NOT VALID check scans none.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090016 amendment 1: a version's capability policy, written by
-- apid at version create and immutable like the rest of the version. NULL
-- allows nothing but the version's models, as before this column existed.
ALTER TABLE agent_versions
    ADD COLUMN IF NOT EXISTS capability_policy jsonb;
DO $policy$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_versions'::regclass
                 AND conname = 'agent_versions_capability_policy_check') THEN
    ALTER TABLE agent_versions
      ADD CONSTRAINT agent_versions_capability_policy_check
      CHECK (capability_policy IS NULL OR jsonb_typeof(capability_policy) = 'object') NOT VALID;
  END IF;
END
$policy$;
COMMENT ON COLUMN agent_versions.capability_policy IS
    'ADR-20261027090016 amendment 1: the tools a run of this version may call over the capability gateway, and its per-run tool-call budget. Written by apid; read by outboundd.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE agent_versions DROP CONSTRAINT IF EXISTS agent_versions_capability_policy_check;
ALTER TABLE agent_versions DROP COLUMN IF EXISTS capability_policy;
-- +goose StatementEnd
