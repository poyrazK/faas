-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- ADR-20261110182236: the reason an agent gave for a request
-- (Gregale-Agent-Reason), shown to the person deciding it and in the agent's
-- activity log. Nullable; the previous release ignores it.
ALTER TABLE agent_approval_requests ADD COLUMN IF NOT EXISTS reason text;
ALTER TABLE agent_activity ADD COLUMN IF NOT EXISTS reason text;

-- +goose StatementBegin
-- NOT VALID: new writes are checked at once; every existing row holds NULL.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_approval_requests_reason_check'
                     AND conrelid = 'agent_approval_requests'::regclass) THEN
        ALTER TABLE agent_approval_requests
            ADD CONSTRAINT agent_approval_requests_reason_check
            CHECK (reason IS NULL OR char_length(reason) BETWEEN 1 AND 280) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_activity_reason_check'
                     AND conrelid = 'agent_activity'::regclass) THEN
        ALTER TABLE agent_activity
            ADD CONSTRAINT agent_activity_reason_check
            CHECK (reason IS NULL OR char_length(reason) BETWEEN 1 AND 280) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE agent_activity DROP CONSTRAINT IF EXISTS agent_activity_reason_check;
ALTER TABLE agent_activity DROP COLUMN IF EXISTS reason;
ALTER TABLE agent_approval_requests DROP CONSTRAINT IF EXISTS agent_approval_requests_reason_check;
ALTER TABLE agent_approval_requests DROP COLUMN IF EXISTS reason;
