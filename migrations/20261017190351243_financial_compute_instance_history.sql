-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-566. Bound native negative-receipt and minute funding lookups by instance.
CREATE INDEX IF NOT EXISTS financial_compute_instance_history ON financial_compute_leases(instance_id,node_id,started_at DESC);
-- +goose StatementEnd

-- +goose Down
DROP INDEX financial_compute_instance_history;

-- migration-compat: contract
-- Financial funding rollout coordinates scheduler, native-owner and meter
-- capabilities with the USD gate; this batch requires a full release.
