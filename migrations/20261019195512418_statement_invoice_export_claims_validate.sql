-- filename: 20261019195512418_statement_invoice_export_claims_validate.sql

-- ADR-20261019180000: validate the export claim checks the previous
-- migration added NOT VALID; validation does not block writes.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_statement_invoice_exports VALIDATE CONSTRAINT usage_statement_invoice_exports_attempt_chk;
ALTER TABLE usage_statement_invoice_exports VALIDATE CONSTRAINT usage_statement_invoice_exports_claim_chk;
ALTER TABLE usage_statement_invoice_exports VALIDATE CONSTRAINT usage_statement_invoice_exports_rejected_chk;

-- +goose Down
-- Validation cannot be undone; the previous migration's down drops the checks.
SELECT 1;
