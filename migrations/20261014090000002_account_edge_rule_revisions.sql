-- +goose Up
-- Ownership is captured at mutation time, without foreign keys: deletion of
-- an app/account must not erase the policy's replay identity. Historical NULL
-- ownership is deliberately not guessed from mutable current app metadata.
ALTER TABLE edge_rule_change_log ADD COLUMN account_ids uuid[];
ALTER TABLE edge_rule_change_log ADD CONSTRAINT edge_rule_change_log_account_ids_check
 CHECK (account_ids IS NULL OR (cardinality(account_ids) > 0 AND array_position(account_ids, NULL) IS NULL));
CREATE INDEX edge_rule_change_log_account_ids_idx ON edge_rule_change_log USING gin (account_ids);
CREATE INDEX edge_rule_change_log_unknown_owner_idx ON edge_rule_change_log (id DESC) WHERE account_ids IS NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION edge_rules_record_change()
RETURNS trigger AS $$
DECLARE
    owners uuid[];
BEGIN
    PERFORM pg_advisory_xact_lock(711901248671::bigint);
    IF TG_OP = 'INSERT' THEN
        owners := ARRAY[NEW.account_id];
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts, account_ids)
        VALUES (NEW.app_id, NEW.id, 'created', ARRAY[NEW.match_host], owners);
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        owners := ARRAY[NEW.account_id];
        IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
            owners := ARRAY[OLD.account_id, NEW.account_id];
        END IF;
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts, account_ids)
        VALUES (NEW.app_id, NEW.id, 'updated', ARRAY[OLD.match_host, NEW.match_host], owners);
        RETURN NEW;
    ELSE
        owners := ARRAY[OLD.account_id];
        INSERT INTO edge_rule_change_log (app_id, rule_id, operation, match_hosts, account_ids)
        VALUES (OLD.app_id, OLD.id, 'deleted', ARRAY[OLD.match_host], owners);
        RETURN OLD;
    END IF;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
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
ALTER TABLE edge_rule_change_log DROP COLUMN account_ids;
