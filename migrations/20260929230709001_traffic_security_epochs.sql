-- filename: 20260929230709001_traffic_security_epochs.sql
-- ADR-531: security generations fence admitted HTTP work independently of
-- ordinary immutable policy. Keep deleted UUID tombstones; no FK cascade may
-- erase a revoke while an older gateway still owns an exchange.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS traffic_security_epochs (
    scope_kind text NOT NULL CHECK (scope_kind IN ('account', 'app', 'deployment')),
    scope_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    revoked boolean NOT NULL,
    reason text NOT NULL CHECK (
        (revoked AND reason IN ('account_suspended', 'account_abuse_hold',
                               'app_deleted', 'deployment_quarantined', 'entity_deleted'))
        OR (NOT revoked AND reason = 'released')
    ),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (scope_kind, scope_id)
);

CREATE OR REPLACE FUNCTION advance_traffic_security_epoch(kind text, identity uuid, blocked boolean, cause text)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE applied_revision bigint;
BEGIN
    INSERT INTO traffic_security_epochs(scope_kind, scope_id, revision, revoked, reason)
    VALUES (kind, identity, 1, blocked, cause)
    ON CONFLICT (scope_kind, scope_id) DO UPDATE
    SET revision = traffic_security_epochs.revision + 1,
        revoked = EXCLUDED.revoked, reason = EXCLUDED.reason,
        updated_at = clock_timestamp()
    RETURNING revision INTO applied_revision;
    PERFORM pg_notify('traffic_security_changed',
        json_build_object('scope_kind', kind, 'scope_id', identity,
                          'revision', applied_revision)::text);
END;
$$;

CREATE OR REPLACE FUNCTION account_traffic_security_epoch_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE blocked boolean; cause text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM advance_traffic_security_epoch('account', OLD.id, true, 'entity_deleted');
        RETURN OLD;
    END IF;
    blocked := NEW.status IN ('suspended', 'deleted_pending') OR NEW.abuse_hold_at IS NOT NULL;
    IF TG_OP = 'INSERT' THEN
        IF NOT blocked THEN RETURN NEW; END IF;
    ELSIF (OLD.status IN ('suspended', 'deleted_pending')) IS NOT DISTINCT FROM
          (NEW.status IN ('suspended', 'deleted_pending'))
          AND (OLD.abuse_hold_at IS NOT NULL) IS NOT DISTINCT FROM (NEW.abuse_hold_at IS NOT NULL) THEN
        RETURN NEW;
    END IF;
    cause := CASE WHEN NEW.abuse_hold_at IS NOT NULL THEN 'account_abuse_hold'
                  WHEN blocked THEN 'account_suspended' ELSE 'released' END;
    PERFORM advance_traffic_security_epoch('account', NEW.id, blocked, cause);
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS accounts_traffic_security_epoch ON accounts;
CREATE TRIGGER accounts_traffic_security_epoch
AFTER INSERT OR UPDATE OF status, abuse_hold_at OR DELETE ON accounts
FOR EACH ROW EXECUTE FUNCTION account_traffic_security_epoch_trigger();

CREATE OR REPLACE FUNCTION app_traffic_security_epoch_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE blocked boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM advance_traffic_security_epoch('app', OLD.id, true, 'entity_deleted');
        RETURN OLD;
    END IF;
    blocked := NEW.status = 'deleted';
    IF TG_OP = 'INSERT' THEN
        IF NOT blocked THEN RETURN NEW; END IF;
    ELSIF (OLD.status = 'deleted') IS NOT DISTINCT FROM blocked THEN
        RETURN NEW;
    END IF;
    PERFORM advance_traffic_security_epoch('app', NEW.id, blocked,
        CASE WHEN blocked THEN 'app_deleted' ELSE 'released' END);
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS apps_traffic_security_epoch ON apps;
CREATE TRIGGER apps_traffic_security_epoch
AFTER INSERT OR UPDATE OF status OR DELETE ON apps
FOR EACH ROW EXECUTE FUNCTION app_traffic_security_epoch_trigger();

CREATE OR REPLACE FUNCTION deployment_traffic_security_epoch_trigger()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE blocked boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM advance_traffic_security_epoch('deployment', OLD.id, true, 'entity_deleted');
        RETURN OLD;
    END IF;
    blocked := COALESCE(NEW.parked_reason = 'security_scan_regressed', false);
    IF TG_OP = 'INSERT' THEN
        IF NOT blocked THEN RETURN NEW; END IF;
    ELSIF COALESCE(OLD.parked_reason = 'security_scan_regressed', false) IS NOT DISTINCT FROM blocked THEN
        RETURN NEW;
    END IF;
    PERFORM advance_traffic_security_epoch('deployment', NEW.id, blocked,
        CASE WHEN blocked THEN 'deployment_quarantined' ELSE 'released' END);
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS deployments_traffic_security_epoch ON deployments;
CREATE TRIGGER deployments_traffic_security_epoch
AFTER INSERT OR UPDATE OF parked_reason OR DELETE ON deployments
FOR EACH ROW EXECUTE FUNCTION deployment_traffic_security_epoch_trigger();

-- Preserve existing generations and tombstones when a missing ledger row is replayed.
-- Seed existing blocked identities before any gateway can verify generation
-- zero. Allowed identities need no row until their first security transition.
INSERT INTO traffic_security_epochs(scope_kind, scope_id, revision, revoked, reason)
SELECT 'account', id, 1, true,
       CASE WHEN abuse_hold_at IS NOT NULL THEN 'account_abuse_hold' ELSE 'account_suspended' END
FROM accounts WHERE status IN ('suspended', 'deleted_pending') OR abuse_hold_at IS NOT NULL
ON CONFLICT (scope_kind, scope_id) DO NOTHING;
INSERT INTO traffic_security_epochs(scope_kind, scope_id, revision, revoked, reason)
SELECT 'app', id, 1, true, 'app_deleted' FROM apps WHERE status = 'deleted'
ON CONFLICT (scope_kind, scope_id) DO NOTHING;
INSERT INTO traffic_security_epochs(scope_kind, scope_id, revision, revoked, reason)
SELECT 'deployment', id, 1, true, 'deployment_quarantined'
FROM deployments WHERE parked_reason = 'security_scan_regressed'
ON CONFLICT (scope_kind, scope_id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_traffic_security_epoch ON deployments;
DROP TRIGGER IF EXISTS apps_traffic_security_epoch ON apps;
DROP TRIGGER IF EXISTS accounts_traffic_security_epoch ON accounts;
DROP FUNCTION IF EXISTS deployment_traffic_security_epoch_trigger();
DROP FUNCTION IF EXISTS app_traffic_security_epoch_trigger();
DROP FUNCTION IF EXISTS account_traffic_security_epoch_trigger();
DROP FUNCTION IF EXISTS advance_traffic_security_epoch(text, uuid, boolean, text);
DROP TABLE IF EXISTS traffic_security_epochs;
-- +goose StatementEnd
