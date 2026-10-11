-- filename: 20261010085840889_edge_rule_mode_and_hit_counts.sql
--
-- ADR-960: edge rules gain mode ('enforce' | 'log'); log-mode rules are
-- matched and counted but never act. Per-rule hit counts are flushed by each
-- gateway once a minute into hourly buckets, kept for 14 days. The ADR-961
-- rule-set snapshot gains the mode column.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rules
    ADD COLUMN IF NOT EXISTS mode text NOT NULL DEFAULT 'enforce';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'edge_rules_mode_chk' AND conrelid = 'edge_rules'::regclass
    ) THEN
        ALTER TABLE edge_rules
            ADD CONSTRAINT edge_rules_mode_chk CHECK (mode IN ('enforce', 'log'));
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS edge_rule_hit_counts (
    rule_id      uuid NOT NULL,
    app_id       uuid NOT NULL,
    bucket_start timestamptz NOT NULL,
    outcome      text NOT NULL CHECK (outcome IN ('matched', 'logged')),
    hits         bigint NOT NULL CHECK (hits >= 0),
    PRIMARY KEY (rule_id, bucket_start, outcome)
);

CREATE INDEX IF NOT EXISTS edge_rule_hit_counts_app_bucket_idx
    ON edge_rule_hit_counts (app_id, bucket_start);

CREATE OR REPLACE FUNCTION edge_rule_set_snapshot(target uuid)
RETURNS jsonb AS $$
    SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id), '[]'::jsonb)
    FROM (
        SELECT id, account_id, app_id, match_host, match_path, match_methods,
               match_headers, priority, enabled, kind, action, validate_mode,
               cors_preset_id, manifest_key, name, description, expires_at,
               created_at, match_expr, mode
        FROM edge_rules
        WHERE app_id = target
    ) r;
$$ LANGUAGE sql STABLE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION edge_rule_set_snapshot(target uuid)
RETURNS jsonb AS $$
    SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY r.id), '[]'::jsonb)
    FROM (
        SELECT id, account_id, app_id, match_host, match_path, match_methods,
               match_headers, priority, enabled, kind, action, validate_mode,
               cors_preset_id, manifest_key, name, description, expires_at,
               created_at, match_expr
        FROM edge_rules
        WHERE app_id = target
    ) r;
$$ LANGUAGE sql STABLE;
DROP TABLE IF EXISTS edge_rule_hit_counts;
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_mode_chk;
ALTER TABLE edge_rules DROP COLUMN IF EXISTS mode;
-- +goose StatementEnd
