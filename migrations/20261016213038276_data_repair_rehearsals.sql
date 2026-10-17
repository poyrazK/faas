-- filename: 20261016213038276_data_repair_rehearsals.sql

-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE data_repair_rehearsals (
 id uuid PRIMARY KEY,
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 node_id uuid REFERENCES compute_nodes(id) ON DELETE RESTRICT,
 phase text NOT NULL CHECK (phase IN ('queued','running','cleanup','publishing','succeeded','failed','cancelled')),
 cancel_requested boolean NOT NULL DEFAULT false,
 deadline timestamptz,
 record jsonb NOT NULL CHECK (jsonb_typeof(record)='object' AND record->>'id'=id::text AND record->>'operation_id'=operation_id::text AND record->>'account_id'=account_id::text AND record->>'phase'=phase),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((phase='queued' AND node_id IS NULL AND deadline IS NULL) OR phase IN ('failed','cancelled') OR (node_id IS NOT NULL AND deadline IS NOT NULL))
);
CREATE UNIQUE INDEX data_repair_rehearsals_one_active_operation ON data_repair_rehearsals(operation_id) WHERE phase IN ('queued','running','cleanup','publishing');
CREATE UNIQUE INDEX data_repair_rehearsals_one_active_account ON data_repair_rehearsals(account_id) WHERE phase IN ('queued','running','cleanup','publishing');
CREATE UNIQUE INDEX data_repair_rehearsals_one_active_node ON data_repair_rehearsals(node_id) WHERE phase IN ('running','cleanup');
CREATE INDEX data_repair_rehearsals_queue ON data_repair_rehearsals(id) WHERE phase='queued';
CREATE INDEX data_repair_rehearsals_recovery ON data_repair_rehearsals(node_id,id) WHERE phase IN ('running','cleanup','publishing');

-- +goose StatementBegin
CREATE FUNCTION guard_data_repair_rehearsal_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.phase IN ('queued','running','cleanup','publishing') THEN
  RAISE EXCEPTION 'repair_rehearsal_cleanup_required' USING ERRCODE='55000';
 END IF;
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER data_repair_rehearsals_require_cleanup BEFORE DELETE ON data_repair_rehearsals
FOR EACH ROW EXECUTE FUNCTION guard_data_repair_rehearsal_delete();
