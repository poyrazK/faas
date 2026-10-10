-- ADR-517: pin workflow contract versions and retain transition evidence.
-- +goose Up
ALTER TABLE customer_operation_workflow_state_reports
    ADD COLUMN IF NOT EXISTS contract_version integer NOT NULL DEFAULT 1
    CHECK (contract_version BETWEEN 1 AND 1000000),
    ADD COLUMN IF NOT EXISTS evidence_milestones jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(evidence_milestones) = 'array' AND jsonb_array_length(evidence_milestones) <= 16);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM customer_operation_workflow_state_reports
        WHERE contract_version <> 1 OR evidence_milestones <> '[]'::jsonb
    ) THEN
        RAISE EXCEPTION 'retained versioned workflow evidence prevents rollback';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE customer_operation_workflow_state_reports
    DROP COLUMN IF EXISTS evidence_milestones,
    DROP COLUMN IF EXISTS contract_version;
