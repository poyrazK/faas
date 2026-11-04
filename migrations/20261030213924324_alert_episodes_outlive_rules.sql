-- filename: 20261030213924324_alert_episodes_outlive_rules.sql
-- migration-compat: contract
-- ADR-20261030214500: an alert episode, and the incident notes written on it,
-- outlive its rule. Deleting a rule (directly, or through its SLO, synthetic
-- check or app) used to cascade its episodes and their notes away, though
-- notes are documented as permanent. A BEFORE DELETE trigger on alert_rules
-- now copies the rule's name, metric and app onto its episodes and resolves
-- an open one, so every delete path keeps them; the episodes' foreign key to
-- alert_rules goes, which is why this is a contract migration: the previous
-- release's episode reads join alert_rules and simply do not list episodes
-- whose rule is gone.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_episodes
    ADD COLUMN IF NOT EXISTS rule_name text,
    ADD COLUMN IF NOT EXISTS rule_metric text,
    ADD COLUMN IF NOT EXISTS app_id uuid;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION alert_episodes_keep_deleted_rule() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE alert_episodes
       SET rule_name = OLD.name,
           rule_metric = OLD.metric,
           app_id = OLD.app_id,
           resolved_at = coalesce(resolved_at, greatest(now(), opened_at))
     WHERE rule_id = OLD.id;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS alert_rules_keep_episodes ON alert_rules;
CREATE TRIGGER alert_rules_keep_episodes BEFORE DELETE ON alert_rules
    FOR EACH ROW EXECUTE FUNCTION alert_episodes_keep_deleted_rule();
ALTER TABLE alert_episodes DROP CONSTRAINT IF EXISTS alert_episodes_rule_id_fkey;

-- +goose Down
-- Restoring the foreign key fails while episodes of deleted rules remain;
-- delete them first.
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS alert_rules_keep_episodes ON alert_rules;
DROP FUNCTION IF EXISTS alert_episodes_keep_deleted_rule();
DELETE FROM alert_episodes e WHERE NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.id = e.rule_id);
ALTER TABLE alert_episodes
    ADD CONSTRAINT alert_episodes_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES alert_rules(id) ON DELETE CASCADE;
ALTER TABLE alert_episodes
    DROP COLUMN IF EXISTS app_id,
    DROP COLUMN IF EXISTS rule_metric,
    DROP COLUMN IF EXISTS rule_name;
