-- +goose Up
ALTER TABLE deployment_mcp_task_holds
    ADD COLUMN IF NOT EXISTS first_held_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN IF NOT EXISTS recovery_channel text NOT NULL DEFAULT '' CHECK (recovery_channel IN ('', 'snapshot_written', 'deployment_ready')),
    ADD COLUMN IF NOT EXISTS recovery_payload jsonb CHECK (octet_length(recovery_payload::text) <= 65536),
    ADD COLUMN IF NOT EXISTS owner_node text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN IF NOT EXISTS lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wake_requested boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS deployment_mcp_task_holds_due_idx ON deployment_mcp_task_holds (next_attempt_at)
    WHERE recovery_payload IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS deployment_mcp_task_holds_due_idx;
ALTER TABLE deployment_mcp_task_holds DROP COLUMN IF EXISTS first_held_at, DROP COLUMN IF EXISTS recovery_channel,
    DROP COLUMN IF EXISTS recovery_payload, DROP COLUMN IF EXISTS owner_node, DROP COLUMN IF EXISTS attempts,
    DROP COLUMN IF EXISTS next_attempt_at, DROP COLUMN IF EXISTS lease_until, DROP COLUMN IF EXISTS claim_generation, DROP COLUMN IF EXISTS wake_requested;
