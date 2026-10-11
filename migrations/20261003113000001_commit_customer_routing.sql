-- +goose Up
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS contract_version integer NOT NULL DEFAULT 1;
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS allow_tenant_selection boolean NOT NULL DEFAULT false;
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS routing jsonb;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commit_sources'::regclass AND conname='commit_sources_contract_version_check') THEN
        ALTER TABLE commit_sources ADD CONSTRAINT commit_sources_contract_version_check CHECK (contract_version IN (1,2) AND (NOT allow_tenant_selection OR contract_version=2));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='commit_receipts'::regclass AND conname='commit_receipts_routing_check') THEN
        ALTER TABLE commit_receipts ADD CONSTRAINT commit_receipts_routing_check CHECK (
            routing IS NULL OR COALESCE(jsonb_typeof(routing)='object' AND routing->'version'='2'::jsonb
            AND jsonb_typeof(routing->'key') IN ('string','number','boolean'),false));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Routing authority and accepted identities are durable; rollback is intentionally unsupported.
SELECT 1;
