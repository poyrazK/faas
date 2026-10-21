-- migration-compat: expand
-- Deployment attempt request fingerprints (ADR-20261021101500). A receipt
-- records what its attempt asked for, so a reused attempt ID with a different
-- request is refused instead of replaying the earlier deployment. Receipts
-- accepted before this column existed keep NULL and replay as before.
-- Applied online (ADR-202610171900): every goose statement is its own short
-- transaction on one existing table. Every step is idempotent.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE deployment_submission_receipts ADD COLUMN IF NOT EXISTS request_fingerprint text;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_submission_receipts'::regclass AND conname='deployment_submission_receipts_request_fingerprint_check') THEN
  ALTER TABLE deployment_submission_receipts ADD CONSTRAINT deployment_submission_receipts_request_fingerprint_check CHECK (request_fingerprint ~ '^[0-9a-f]{64}$') NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='deployment_submission_receipts'::regclass AND conname='deployment_submission_receipts_request_fingerprint_check' AND NOT convalidated) THEN
  ALTER TABLE deployment_submission_receipts VALIDATE CONSTRAINT deployment_submission_receipts_request_fingerprint_check;
 END IF;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
-- +goose Down
ALTER TABLE deployment_submission_receipts DROP COLUMN IF EXISTS request_fingerprint;
