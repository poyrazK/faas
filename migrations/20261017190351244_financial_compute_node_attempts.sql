-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-566. Unbudgeted migration may overlap a paused source and destination.
-- Strict acquisition separately serializes/rejects that overlap per account.
DROP INDEX IF EXISTS financial_compute_one_live_instance;
CREATE UNIQUE INDEX IF NOT EXISTS financial_compute_one_live_instance ON financial_compute_leases(instance_id,node_id) WHERE settled_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- Refuse rollback while any physical overlap remains; never discard history.
DROP INDEX financial_compute_one_live_instance;
CREATE UNIQUE INDEX financial_compute_one_live_instance ON financial_compute_leases(instance_id) WHERE settled_at IS NULL;
