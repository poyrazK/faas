-- filename: 20261027141120295_trigger_agent_targets.sql
-- migration-compat: expand

-- +goose Up
-- Adding the column and the NOT VALID CHECK takes a brief lock on triggers;
-- 20261027141127468 validates the check without blocking schedd's writes.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090010 §9: a broker trigger may target an agent instead of its app's
-- function. schedd admits one agent run per record; apid sets the agent on a
-- just-created, disabled trigger, as it sets batch delivery (ADR-1321).
ALTER TABLE triggers ADD COLUMN IF NOT EXISTS agent text;
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'triggers'::regclass
        AND conname = 'triggers_agent_check') THEN
        ALTER TABLE triggers ADD CONSTRAINT triggers_agent_check CHECK (
            agent IS NULL OR (agent ~ '^[a-z][a-z0-9-]{0,62}$'
                AND kind IN ('kafka', 'nats', 'redis_streams', 'sqs_compat')
                AND delivery = 'record' AND queue_binding_id IS NULL)) NOT VALID;
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM triggers WHERE agent IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot discard the agent targets of existing triggers';
    END IF;
END $$;
ALTER TABLE triggers DROP CONSTRAINT IF EXISTS triggers_agent_check, DROP COLUMN IF EXISTS agent;
-- +goose StatementEnd
