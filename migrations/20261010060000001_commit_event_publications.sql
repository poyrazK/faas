-- +goose Up
ALTER TABLE commit_sources ADD COLUMN IF NOT EXISTS destination text NOT NULL DEFAULT 'operation'
 CHECK (destination IN ('operation','event'));
ALTER TABLE commit_sources DROP CONSTRAINT IF EXISTS commit_sources_contract_version_check;
ALTER TABLE commit_sources ADD CONSTRAINT commit_sources_contract_version_check CHECK (
 (destination='operation' AND contract_version IN (1,2) AND (NOT allow_tenant_selection OR contract_version=2))
 OR (destination='event' AND contract_version=3 AND NOT allow_tenant_selection AND operation_policy IS NULL));
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS publication_acceptance_id bigint CHECK (publication_acceptance_id>0);
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS publication_source text;
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS publication_event_id text;
ALTER TABLE commit_receipts ADD COLUMN IF NOT EXISTS schema_version text NOT NULL DEFAULT '';
ALTER TABLE commit_receipts DROP CONSTRAINT IF EXISTS commit_receipt_one_operation;
ALTER TABLE commit_receipts ADD CONSTRAINT commit_receipt_one_operation CHECK (
 (invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer
 + (publication_acceptance_id IS NOT NULL)::integer = 1);
ALTER TABLE commit_receipts DROP CONSTRAINT IF EXISTS commit_receipt_publication_identity;
ALTER TABLE commit_receipts ADD CONSTRAINT commit_receipt_publication_identity CHECK (
 (publication_acceptance_id IS NULL AND publication_source IS NULL AND publication_event_id IS NULL)
 OR (publication_acceptance_id IS NOT NULL AND publication_source IS NOT NULL AND publication_event_id IS NOT NULL
 AND routing IS NULL AND length(publication_source)>0 AND length(publication_event_id)>0));
-- No fanout FK: normal event retention must not reopen Commit identity.

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM commit_sources WHERE destination='event')
 OR EXISTS(SELECT 1 FROM commit_receipts WHERE publication_acceptance_id IS NOT NULL) THEN
  RAISE EXCEPTION 'event publication sources and receipts prevent this downgrade';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE commit_receipts DROP CONSTRAINT commit_receipt_publication_identity;
ALTER TABLE commit_receipts DROP CONSTRAINT commit_receipt_one_operation;
ALTER TABLE commit_receipts ADD CONSTRAINT commit_receipt_one_operation CHECK (
 (invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer = 1);
ALTER TABLE commit_receipts DROP COLUMN publication_acceptance_id, DROP COLUMN publication_source,
 DROP COLUMN publication_event_id, DROP COLUMN schema_version;
ALTER TABLE commit_sources DROP CONSTRAINT commit_sources_contract_version_check;
ALTER TABLE commit_sources ADD CONSTRAINT commit_sources_contract_version_check CHECK (
 contract_version IN (1,2) AND (NOT allow_tenant_selection OR contract_version=2));
ALTER TABLE commit_sources DROP COLUMN destination;
