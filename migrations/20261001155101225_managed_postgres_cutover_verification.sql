-- filename: 20261001155101225_managed_postgres_cutover_verification.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT IF EXISTS managed_postgres_cutovers_state_check;
ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_state_check
 CHECK (state IN ('preparing','prepared','verifying','verified','cancelling','cancelled'));
ALTER TABLE managed_postgres_cutovers ADD COLUMN IF NOT EXISTS verified_at timestamptz;
ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT IF EXISTS managed_postgres_cutovers_verified_at_check;
ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_verified_at_check
 CHECK ((state='verified')=(verified_at IS NOT NULL));
ALTER TABLE managed_postgres_cutover_credentials ADD COLUMN IF NOT EXISTS verified_at timestamptz;
ALTER TABLE managed_postgres_cutover_credentials DROP CONSTRAINT IF EXISTS managed_postgres_cutover_credentials_verified_at_check;
ALTER TABLE managed_postgres_cutover_credentials ADD CONSTRAINT managed_postgres_cutover_credentials_verified_at_check
 CHECK (verified_at IS NULL OR state='sealed');
DROP INDEX IF EXISTS managed_postgres_cutovers_due_idx;
CREATE INDEX IF NOT EXISTS managed_postgres_cutovers_due_idx ON managed_postgres_cutovers(retry_at,id)
 WHERE state IN ('preparing','verifying','cancelling');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM managed_postgres_cutovers WHERE state IN ('verifying','verified')) THEN
  RAISE EXCEPTION 'cancel verification and revoke staged credentials before rollback'
   USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
 END IF;
END $$;
ALTER TABLE managed_postgres_cutover_credentials DROP COLUMN verified_at;
ALTER TABLE managed_postgres_cutovers DROP COLUMN verified_at;
ALTER TABLE managed_postgres_cutovers DROP CONSTRAINT managed_postgres_cutovers_state_check;
ALTER TABLE managed_postgres_cutovers ADD CONSTRAINT managed_postgres_cutovers_state_check
 CHECK (state IN ('preparing','prepared','cancelling','cancelled'));
DROP INDEX managed_postgres_cutovers_due_idx;
CREATE INDEX managed_postgres_cutovers_due_idx ON managed_postgres_cutovers(retry_at,id)
 WHERE state IN ('preparing','cancelling');
-- +goose StatementEnd
