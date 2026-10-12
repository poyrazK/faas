-- +goose Up
ALTER TABLE deployment_mcp_task_holds
    ADD COLUMN first_held_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN recovery_channel text NOT NULL DEFAULT '' CHECK (recovery_channel IN ('', 'snapshot_written', 'deployment_ready')),
    ADD COLUMN recovery_payload jsonb CHECK (octet_length(recovery_payload::text) <= 65536),
    ADD COLUMN owner_node text NOT NULL DEFAULT '',
    ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN claim_generation bigint NOT NULL DEFAULT 0,
    ADD COLUMN wake_requested boolean NOT NULL DEFAULT false;
CREATE INDEX deployment_mcp_task_holds_due_idx ON deployment_mcp_task_holds (next_attempt_at)
    WHERE recovery_payload IS NOT NULL;

-- +goose Down
DROP INDEX deployment_mcp_task_holds_due_idx;
ALTER TABLE deployment_mcp_task_holds DROP COLUMN first_held_at, DROP COLUMN recovery_channel,
    DROP COLUMN recovery_payload, DROP COLUMN owner_node, DROP COLUMN attempts,
    DROP COLUMN next_attempt_at, DROP COLUMN lease_until, DROP COLUMN claim_generation, DROP COLUMN wake_requested;
