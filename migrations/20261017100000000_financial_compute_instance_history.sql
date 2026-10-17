-- +goose Up
-- ADR-566. Bound native negative-receipt and minute funding lookups by instance.
CREATE INDEX financial_compute_instance_history ON financial_compute_leases(instance_id,node_id,started_at DESC);

-- +goose Down
DROP INDEX financial_compute_instance_history;
