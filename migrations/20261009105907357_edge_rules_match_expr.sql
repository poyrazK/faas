-- filename: 20261009105907357_edge_rules_match_expr.sql
--
-- ADR-906: an optional structured match condition on every edge rule. The
-- application validates and bounds the expression; the database only pins
-- its outer shape. The ADR-905 rule-set snapshot gains the column so
-- versions and rollback carry it.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE edge_rules
    ADD COLUMN IF NOT EXISTS match_expr jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'edge_rules_match_expr_shape_chk' AND conrelid = 'edge_rules'::regclass
    ) THEN
        ALTER TABLE edge_rules
            ADD CONSTRAINT edge_rules_match_expr_shape_chk
            CHECK (match_expr IS NULL OR (jsonb_typeof(match_expr) = 'object' AND octet_length(match_expr::text) <= 65536));
    END IF;
END
$$;

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
               created_at
        FROM edge_rules
        WHERE app_id = target
    ) r;
$$ LANGUAGE sql STABLE;
ALTER TABLE edge_rules DROP CONSTRAINT IF EXISTS edge_rules_match_expr_shape_chk;
ALTER TABLE edge_rules DROP COLUMN IF EXISTS match_expr;
-- +goose StatementEnd
