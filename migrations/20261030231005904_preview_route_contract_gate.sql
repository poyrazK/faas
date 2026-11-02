-- filename: 20261030231005904_preview_route_contract_gate.sql
-- migration-compat: expand
--
-- ADR-20261031090000: a production app's declared-route contract
-- (only_declared_routes, declared_routes) follows onto its PR previews like
-- its other access gates. Without it a preview answered every path on its
-- public host, including the ones production's contract hides. The trigger
-- gains the two columns; open previews take their production app's contract
-- now, a batch per transaction. Older writers leave both columns alone. Every
-- step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_preview_access_gates() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    preview_id uuid;
BEGIN
    -- A preview runs unreviewed code on a guessable host; it keeps the
    -- access, egress and route gates of the production app it previews.
    FOR preview_id IN
        UPDATE apps
        SET require_authn = NEW.require_authn,
            public_auth_mode = NEW.public_auth_mode,
            public_auth_basic = NEW.public_auth_basic,
            public_auth_ip_allowlist = NEW.public_auth_ip_allowlist,
            consumer_auth_mode = NEW.consumer_auth_mode,
            require_signed = NEW.require_signed,
            egress_allowlist = NEW.egress_allowlist,
            visibility = NEW.visibility,
            only_declared_routes = NEW.only_declared_routes,
            declared_routes = NEW.declared_routes
        WHERE account_id = NEW.account_id
          AND preview_of_slug = NEW.slug
          AND status <> 'deleted'
        RETURNING id
    LOOP
        PERFORM pg_notify('app_changed', preview_id::text);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_sync_preview_access_gates ON apps;

CREATE TRIGGER apps_sync_preview_access_gates
    AFTER UPDATE OF require_authn, public_auth_mode, public_auth_basic, public_auth_ip_allowlist,
        consumer_auth_mode, require_signed, egress_allowlist, visibility,
        only_declared_routes, declared_routes ON apps
    FOR EACH ROW
    WHEN (NEW.preview_of_slug IS NULL AND NEW.status <> 'deleted' AND (
        OLD.require_authn IS DISTINCT FROM NEW.require_authn OR
        OLD.public_auth_mode IS DISTINCT FROM NEW.public_auth_mode OR
        OLD.public_auth_basic IS DISTINCT FROM NEW.public_auth_basic OR
        OLD.public_auth_ip_allowlist IS DISTINCT FROM NEW.public_auth_ip_allowlist OR
        OLD.consumer_auth_mode IS DISTINCT FROM NEW.consumer_auth_mode OR
        OLD.require_signed IS DISTINCT FROM NEW.require_signed OR
        OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist OR
        OLD.visibility IS DISTINCT FROM NEW.visibility OR
        OLD.only_declared_routes IS DISTINCT FROM NEW.only_declared_routes OR
        OLD.declared_routes IS DISTINCT FROM NEW.declared_routes))
    EXECUTE FUNCTION sync_preview_access_gates();

-- +goose StatementBegin
DO $backfill$
DECLARE
    changed uuid[];
    preview_id uuid;
BEGIN
    LOOP
        WITH batch AS (
            SELECT preview.id, parent.only_declared_routes, parent.declared_routes
            FROM apps preview
            JOIN apps parent ON parent.account_id = preview.account_id
                AND parent.slug = preview.preview_of_slug
                AND parent.preview_of_slug IS NULL
                AND parent.status <> 'deleted'
            WHERE preview.preview_of_slug IS NOT NULL
              AND preview.status <> 'deleted'
              AND (preview.only_declared_routes, preview.declared_routes)
                  IS DISTINCT FROM (parent.only_declared_routes, parent.declared_routes)
            LIMIT 500
        ), updated AS (
            UPDATE apps
            SET only_declared_routes = batch.only_declared_routes,
                declared_routes = batch.declared_routes
            FROM batch
            WHERE apps.id = batch.id
            RETURNING apps.id
        )
        SELECT coalesce(array_agg(id), '{}') INTO changed FROM updated;
        EXIT WHEN cardinality(changed) = 0;
        FOREACH preview_id IN ARRAY changed LOOP
            PERFORM pg_notify('app_changed', preview_id::text);
        END LOOP;
        COMMIT;
    END LOOP;
END
$backfill$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_preview_access_gates() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    preview_id uuid;
BEGIN
    FOR preview_id IN
        UPDATE apps
        SET require_authn = NEW.require_authn,
            public_auth_mode = NEW.public_auth_mode,
            public_auth_basic = NEW.public_auth_basic,
            public_auth_ip_allowlist = NEW.public_auth_ip_allowlist,
            consumer_auth_mode = NEW.consumer_auth_mode,
            require_signed = NEW.require_signed,
            egress_allowlist = NEW.egress_allowlist,
            visibility = NEW.visibility
        WHERE account_id = NEW.account_id
          AND preview_of_slug = NEW.slug
          AND status <> 'deleted'
        RETURNING id
    LOOP
        PERFORM pg_notify('app_changed', preview_id::text);
    END LOOP;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS apps_sync_preview_access_gates ON apps;

CREATE TRIGGER apps_sync_preview_access_gates
    AFTER UPDATE OF require_authn, public_auth_mode, public_auth_basic, public_auth_ip_allowlist,
        consumer_auth_mode, require_signed, egress_allowlist, visibility ON apps
    FOR EACH ROW
    WHEN (NEW.preview_of_slug IS NULL AND NEW.status <> 'deleted' AND (
        OLD.require_authn IS DISTINCT FROM NEW.require_authn OR
        OLD.public_auth_mode IS DISTINCT FROM NEW.public_auth_mode OR
        OLD.public_auth_basic IS DISTINCT FROM NEW.public_auth_basic OR
        OLD.public_auth_ip_allowlist IS DISTINCT FROM NEW.public_auth_ip_allowlist OR
        OLD.consumer_auth_mode IS DISTINCT FROM NEW.consumer_auth_mode OR
        OLD.require_signed IS DISTINCT FROM NEW.require_signed OR
        OLD.egress_allowlist IS DISTINCT FROM NEW.egress_allowlist OR
        OLD.visibility IS DISTINCT FROM NEW.visibility))
    EXECUTE FUNCTION sync_preview_access_gates();

RESET lock_timeout;
