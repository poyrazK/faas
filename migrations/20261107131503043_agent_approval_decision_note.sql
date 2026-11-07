-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- ADR-20261107140000 §6: the note a person may leave when denying an agent's
-- request, shown to the agent when it repeats the request. The previous
-- release ignores it.
ALTER TABLE agent_approval_requests ADD COLUMN IF NOT EXISTS decision_note text;

-- +goose StatementBegin
-- NOT VALID: new writes are checked at once; every existing row holds NULL.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_approval_requests_decision_note_check') THEN
        ALTER TABLE agent_approval_requests
            ADD CONSTRAINT agent_approval_requests_decision_note_check
            CHECK (decision_note IS NULL OR (status = 'denied' AND char_length(decision_note) BETWEEN 1 AND 280)) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE agent_approval_requests DROP CONSTRAINT IF EXISTS agent_approval_requests_decision_note_check;
ALTER TABLE agent_approval_requests DROP COLUMN IF EXISTS decision_note;
