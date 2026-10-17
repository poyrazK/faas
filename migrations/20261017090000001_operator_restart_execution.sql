-- +goose Up
ALTER TABLE operator_intents
    ADD COLUMN claim_token uuid CHECK (claim_token IS NULL OR claim_token <> '00000000-0000-0000-0000-000000000000'),
    ADD COLUMN completion_audit_outbox_id bigint CHECK (completion_audit_outbox_id IS NULL OR completion_audit_outbox_id > 0);
ALTER TABLE operator_intents DROP CONSTRAINT operator_intents_status_check;
ALTER TABLE operator_intents ADD CONSTRAINT operator_intents_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled', 'outcome_unknown'));

-- A legacy running restart may already have destroyed its VM. Never redispatch it.
UPDATE operator_intents SET status = 'outcome_unknown', finished_at = now(),
    error = 'Legacy restart execution requires operator reconciliation before another action.'
WHERE kind = 'force_restart' AND status = 'running';

CREATE TABLE operator_restart_executions (
    intent_id uuid PRIMARY KEY REFERENCES operator_intents(id) ON DELETE RESTRICT,
    target jsonb NOT NULL CHECK (jsonb_typeof(target) = 'object'),
    phase text NOT NULL CHECK (phase IN ('prepared', 'started', 'destroyed', 'outcome_unknown', 'completed', 'failed')),
    destroy_token uuid CHECK (destroy_token IS NULL OR destroy_token <> '00000000-0000-0000-0000-000000000000'),
    destroyed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (destroyed_at IS NULL OR destroy_token IS NOT NULL),
    CHECK (phase NOT IN ('destroyed', 'completed') OR destroyed_at IS NOT NULL),
    CHECK (phase <> 'started' OR destroy_token IS NOT NULL)
);

CREATE INDEX operator_intents_unknown_restart_idx ON operator_intents (requested_at, id)
    WHERE status = 'outcome_unknown' AND kind = 'force_restart';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM operator_restart_executions)
       OR EXISTS (SELECT 1 FROM operator_intents WHERE status = 'outcome_unknown') THEN
        RAISE EXCEPTION 'Reconcile and preserve operator restart evidence before downgrade';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE operator_restart_executions;
DROP INDEX operator_intents_unknown_restart_idx;
ALTER TABLE operator_intents DROP CONSTRAINT operator_intents_status_check;
ALTER TABLE operator_intents ADD CONSTRAINT operator_intents_status_check
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled'));
ALTER TABLE operator_intents DROP COLUMN completion_audit_outbox_id, DROP COLUMN claim_token;
