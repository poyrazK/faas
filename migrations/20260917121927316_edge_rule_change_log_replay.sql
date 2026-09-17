-- Durable edge-rule mutation ledger.
--
-- LISTEN/NOTIFY is still the low-latency path, but a gateway can be
-- disconnected while a rule changes.  This durable ledger makes that
-- gap observable to every gateway replica.  The table intentionally has no
-- foreign keys: delete cascades on apps/edge_rules must leave a repair trail.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_rule_change_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id      uuid NOT NULL,
    rule_id     uuid NOT NULL,
    operation   text NOT NULL CHECK (operation IN ('created', 'updated', 'deleted')),
    match_hosts text[] NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS edge_rule_change_log_created_idx
    ON edge_rule_change_log (created_at, id);

CREATE OR REPLACE FUNCTION edge_rules_record_change()
RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts)
        VALUES (NEW.app_id, NEW.id, 'created', ARRAY[NEW.match_host]);
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts)
        VALUES (NEW.app_id, NEW.id, 'updated', ARRAY[OLD.match_host, NEW.match_host]);
        RETURN NEW;
    ELSE
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts)
        VALUES (OLD.app_id, OLD.id, 'deleted', ARRAY[OLD.match_host]);
        RETURN OLD;
    END IF;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS edge_rules_record_change_trg ON edge_rules;
CREATE TRIGGER edge_rules_record_change_trg
    AFTER INSERT OR UPDATE OR DELETE ON edge_rules
    FOR EACH ROW EXECUTE FUNCTION edge_rules_record_change();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS edge_rules_record_change_trg ON edge_rules;
DROP FUNCTION IF EXISTS edge_rules_record_change();
DROP TABLE IF EXISTS edge_rule_change_log;
-- +goose StatementEnd
