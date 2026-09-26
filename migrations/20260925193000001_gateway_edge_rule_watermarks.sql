-- +goose Up
-- Each gateway publishes the latest edge-rule ledger entry it has repaired.
-- Keep this cursor independent from the control-plane ledger: the IDs belong
-- to different sequences and must never be compared to each other.
CREATE TABLE IF NOT EXISTS gateway_edge_rule_watermarks (
    node_name       text PRIMARY KEY CHECK (node_name <> ''),
    boot_id         uuid NOT NULL,
    last_change_id  bigint NOT NULL CHECK (last_change_id >= 0),
    observed_at     timestamptz NOT NULL DEFAULT now()
);

-- A replay cursor is safe only if IDs are visible in commit order. Reuse the
-- control-plane ledger's transaction lock so a later edge mutation cannot
-- become visible while an earlier ID is still uncommitted.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION edge_rules_record_change()
RETURNS trigger AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(711901248671::bigint);
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
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS gateway_edge_rule_watermarks;

-- Restore the original trigger body. The ledger itself remains owned by its
-- original migration.
-- +goose StatementBegin
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
-- +goose StatementEnd
