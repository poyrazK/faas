-- filename: 20261020104451176_alert_rules_edge_rule_activity_metric.sql
--
-- ADR-1192 amendment: edge_rule_activity counts an edge rule's matches in
-- either mode (enforced + log-mode), so an alert created with a protection
-- recipe (ADR-986) keeps working when the rule moves from log mode to
-- enforce. Widens alert_rules_metric_chk and alert_rules_edge_rule_chk from
-- their current definitions, the way the MCP and issue alert migrations
-- widen their vocabularies; Down removes exactly what Up added. Widening
-- only admits more rows, so every existing row still satisfies both.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_base text; edge_base text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO rules_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO edge_base FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_edge_rule_chk';
 IF rules_base IS NULL OR edge_base IS NULL THEN RAISE EXCEPTION 'missing alert_rules metric constraints'; END IF;
 IF position('edge_rule_activity' in rules_base) > 0 THEN RETURN; END IF;
 IF position('''edge_rule_log_hits''::text]' in edge_base) = 0 THEN RAISE EXCEPTION 'unrecognized alert_rules_edge_rule_chk: %', edge_base; END IF;
 EXECUTE 'ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk';
 EXECUTE format('ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((%s) OR metric = %L)', regexp_replace(rules_base, '^CHECK \((.*)\)$', '\1'), 'edge_rule_activity');
 EXECUTE 'ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_edge_rule_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_rule_chk ' || replace(edge_base, '''edge_rule_log_hits''::text]', '''edge_rule_log_hits''::text, ''edge_rule_activity''::text]');
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ DECLARE rules_now text; edge_now text;
BEGIN
 IF EXISTS (SELECT 1 FROM alert_rules WHERE metric = 'edge_rule_activity') THEN
  RAISE EXCEPTION 'cannot roll back: edge_rule_activity alert rules exist';
 END IF;
 SELECT pg_get_constraintdef(oid) INTO rules_now FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk';
 SELECT pg_get_constraintdef(oid) INTO edge_now FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_edge_rule_chk';
 EXECUTE 'ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk ' || replace(rules_now, ' OR (metric = ''edge_rule_activity''::text)', '');
 EXECUTE 'ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_edge_rule_chk';
 EXECUTE 'ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_edge_rule_chk ' || replace(edge_now, ', ''edge_rule_activity''::text]', ']');
END $$;
-- +goose StatementEnd
