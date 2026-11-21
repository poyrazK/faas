-- migration-compat: expand
-- Verification opens stay on the runtime key owner and are audited before SQL.
-- +goose Up
CREATE TABLE IF NOT EXISTS secret_verification_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL, app_id uuid NOT NULL, cutover_id uuid NOT NULL,
 credential_id uuid NOT NULL, verified_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS secret_verification_audit_cutover_idx ON secret_verification_audit(cutover_id,verified_at);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON secret_verification_audit FROM PUBLIC;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON secret_verification_audit FROM faas_runtime;
 END IF;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='faas_secretd') THEN
  GRANT SELECT, INSERT ON secret_verification_audit TO faas_secretd;
  GRANT USAGE ON SEQUENCE secret_verification_audit_id_seq TO faas_secretd;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE IF EXISTS secret_verification_audit;
