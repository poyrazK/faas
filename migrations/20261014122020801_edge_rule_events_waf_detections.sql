-- filename: 20261014122020801_edge_rule_events_waf_detections.sql
--
-- ADR-831 amendment 5: kind=waf detections are ADR-964 security events.
-- New outcomes: detected (off-path sample), warned and blocked (in-path).
-- A detection records the CRS rule IDs that scored, their attack categories
-- and the anomaly score; never the matched payload or the query string.
-- Every statement is re-runnable (TestNewMigrationsAreReplaySafe).

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rule_events ADD COLUMN IF NOT EXISTS waf_rule_ids integer[] NOT NULL DEFAULT '{}';
ALTER TABLE edge_rule_events ADD COLUMN IF NOT EXISTS waf_categories text[] NOT NULL DEFAULT '{}';
ALTER TABLE edge_rule_events ADD COLUMN IF NOT EXISTS waf_score integer NOT NULL DEFAULT 0;

ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check;
ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_outcome_check
    CHECK (outcome IN ('matched', 'logged', 'detected', 'warned', 'blocked'));

ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_waf_chk;
ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_waf_chk CHECK (
    cardinality(waf_rule_ids) <= 32
    AND cardinality(waf_categories) <= 16
    AND waf_score BETWEEN 0 AND 10000
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM edge_rule_events WHERE outcome IN ('detected', 'warned', 'blocked');
ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_waf_chk;
ALTER TABLE edge_rule_events DROP CONSTRAINT IF EXISTS edge_rule_events_outcome_check;
ALTER TABLE edge_rule_events ADD CONSTRAINT edge_rule_events_outcome_check
    CHECK (outcome IN ('matched', 'logged'));
ALTER TABLE edge_rule_events DROP COLUMN IF EXISTS waf_score;
ALTER TABLE edge_rule_events DROP COLUMN IF EXISTS waf_categories;
ALTER TABLE edge_rule_events DROP COLUMN IF EXISTS waf_rule_ids;
-- +goose StatementEnd
