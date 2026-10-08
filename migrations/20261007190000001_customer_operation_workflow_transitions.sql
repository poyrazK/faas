-- filename: 20261007190000001_customer_operation_workflow_transitions.sql
-- ADR-720: retain the declared source state with each transition report.
-- Replay-safety: a missing ledger row leaves the existing column in place.
-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
    ADD COLUMN IF NOT EXISTS from_state text NOT NULL DEFAULT ''
    CHECK (from_state = '' OR from_state ~ '^[a-z][a-z0-9-]{0,63}$');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM customer_operation_workflow_state_reports WHERE from_state <> '') THEN
        RAISE EXCEPTION 'retained customer workflow transitions prevent rollback';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE customer_operation_workflow_state_reports DROP COLUMN IF EXISTS from_state;
