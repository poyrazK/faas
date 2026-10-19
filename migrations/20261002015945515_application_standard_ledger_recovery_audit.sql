-- filename: 20261002015945515_application_standard_ledger_recovery_audit.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS application_standard_ledger_recoveries (
 approval_hash text PRIMARY KEY CHECK (approval_hash ~ '^[a-f0-9]{64}$'),
 target_hash text NOT NULL CHECK (target_hash ~ '^[a-f0-9]{64}$'),
 schema_hash text NOT NULL CHECK (schema_hash ~ '^[a-f0-9]{64}$'),
 source_hash text NOT NULL CHECK (source_hash ~ '^[a-f0-9]{64}$'),
 ledger_hash text NOT NULL CHECK (ledger_hash ~ '^[a-f0-9]{64}$'),
 actor text NOT NULL DEFAULT current_user CHECK (actor <> ''),
 plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object'),
 repaired_versions bigint[] NOT NULL CHECK (cardinality(repaired_versions) > 0),
 recovered_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE OR REPLACE FUNCTION application_standard_ledger_recovery_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'ledger recovery evidence is immutable'
  USING ERRCODE='23514',CONSTRAINT='application_standard_ledger_recovery_immutable';
END;
$$;
DROP TRIGGER IF EXISTS application_standard_ledger_recovery_immutable ON application_standard_ledger_recoveries;
CREATE TRIGGER application_standard_ledger_recovery_immutable BEFORE UPDATE OR DELETE
 ON application_standard_ledger_recoveries FOR EACH ROW EXECUTE FUNCTION application_standard_ledger_recovery_immutable();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS application_standard_ledger_recoveries;
DROP FUNCTION IF EXISTS application_standard_ledger_recovery_immutable();
-- +goose StatementEnd
