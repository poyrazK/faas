-- +goose Up
-- CORS presets are account-scoped shared policy. Keep their mutation history
-- and each serving gateway's applied position separate from the app-scoped
-- edge-rule ledger.
CREATE TABLE IF NOT EXISTS cors_preset_change_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id  uuid NOT NULL,
    preset_id   uuid NOT NULL,
    operation   text NOT NULL CHECK (operation IN ('created', 'updated', 'deleted')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cors_preset_change_log_account_idx
    ON cors_preset_change_log (account_id, id);
CREATE INDEX IF NOT EXISTS cors_preset_change_log_created_idx
    ON cors_preset_change_log (created_at, id);

CREATE TABLE IF NOT EXISTS gateway_cors_preset_watermarks (
    node_name       text PRIMARY KEY CHECK (node_name <> ''),
    boot_id         uuid NOT NULL,
    last_change_id  bigint NOT NULL CHECK (last_change_id >= 0),
    observed_at     timestamptz NOT NULL DEFAULT now()
);

-- Serialize IDs through commit so MAX(id) is a safe replay/status cursor.
-- Keep pg_notify as the low-latency path; both effects commit atomically.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cors_presets_changed_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(711901248671::bigint);

    IF TG_OP = 'DELETE' THEN
        INSERT INTO cors_preset_change_log (account_id, preset_id, operation)
        VALUES (OLD.account_id, OLD.id, 'deleted');
        PERFORM pg_notify('cors_preset_changed', OLD.account_id::text);
        RETURN OLD;
    ELSIF TG_OP = 'INSERT' THEN
        INSERT INTO cors_preset_change_log (account_id, preset_id, operation)
        VALUES (NEW.account_id, NEW.id, 'created');
        PERFORM pg_notify('cors_preset_changed', NEW.account_id::text);
        RETURN NEW;
    END IF;

    IF OLD.account_id IS DISTINCT FROM NEW.account_id THEN
        INSERT INTO cors_preset_change_log (account_id, preset_id, operation)
        VALUES (OLD.account_id, OLD.id, 'updated');
        PERFORM pg_notify('cors_preset_changed', OLD.account_id::text);
    END IF;
    INSERT INTO cors_preset_change_log (account_id, preset_id, operation)
    VALUES (NEW.account_id, NEW.id, 'updated');
    PERFORM pg_notify('cors_preset_changed', NEW.account_id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS cors_presets_changed_notify_trg ON cors_presets;
CREATE TRIGGER cors_presets_changed_notify_trg
AFTER INSERT OR UPDATE OR DELETE ON cors_presets
FOR EACH ROW
EXECUTE FUNCTION cors_presets_changed_notify();

-- +goose Down
DROP TRIGGER IF EXISTS cors_presets_changed_notify_trg ON cors_presets;

-- Restore the notification-only trigger body owned by migration 00487.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cors_presets_changed_notify() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('cors_preset_changed', OLD.account_id::text);
        RETURN OLD;
    END IF;
    PERFORM pg_notify('cors_preset_changed', NEW.account_id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER cors_presets_changed_notify_trg
AFTER INSERT OR UPDATE OR DELETE ON cors_presets
FOR EACH ROW
EXECUTE FUNCTION cors_presets_changed_notify();

DROP TABLE IF EXISTS gateway_cors_preset_watermarks;
DROP TABLE IF EXISTS cors_preset_change_log;
