-- filename: 20261017201035893_alert_rules_slo_synthetic_indexes.sql
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-747 / ADR-748: partial indexes for SLO and synthetic-check alert rules.
-- Built CONCURRENTLY so the build never blocks alert_rules writes; the
-- columns come from 20261014141851389 and 20261014141851533.
CREATE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_slo_id_idx ON alert_rules (slo_id) WHERE slo_id IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS alert_rules_synthetic_check_id_idx ON alert_rules (synthetic_check_id) WHERE synthetic_check_id IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_synthetic_check_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_slo_id_idx;
