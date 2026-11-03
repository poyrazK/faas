-- filename: 20261031152352664_preview_tenant_security_gates.sql
-- migration-compat: expand
--
-- ADR-20261031180000: a preview keeps its production app's
-- platform_tenant_required gate and security_policy deploy guard like its
-- other gates. A tenant-only app's preview answered requests without tenant
-- attribution. The trigger gains the two columns, for the previews of an app
-- and for the services its PR environments add; existing previews take them
-- now, a batch per transaction. Older writers leave both columns alone.
-- Every step is re-runnable.
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
            declared_routes = NEW.declared_routes,
            platform_tenant_required = NEW.platform_tenant_required,
            security_policy = NEW.security_policy
        WHERE account_id = NEW.account_id
          AND preview_of_slug = NEW.slug
          AND status <> 'deleted'
        RETURNING id
    LOOP
        PERFORM pg_notify('app_changed', preview_id::text);
    END LOOP;
    -- A service the PR adds has no production app; its preview keeps the
    -- access and egress gates of the app the PR previews (the root of its
    -- PR environment), not that app's visibility or route contract.
    FOR preview_id IN
        UPDATE apps service
        SET require_authn = NEW.require_authn,
            public_auth_mode = NEW.public_auth_mode,
            public_auth_basic = NEW.public_auth_basic,
            public_auth_ip_allowlist = NEW.public_auth_ip_allowlist,
            consumer_auth_mode = NEW.consumer_auth_mode,
            require_signed = NEW.require_signed,
            egress_allowlist = NEW.egress_allowlist,
            platform_tenant_required = NEW.platform_tenant_required,
            security_policy = NEW.security_policy
        FROM pr_preview_sets preview_set
        JOIN apps root ON root.id = preview_set.root_app_id
        WHERE root.account_id = NEW.account_id
          AND root.preview_of_slug = NEW.slug
          AND preview_set.member_app_ids @> ARRAY[service.id::text]
          AND service.id <> root.id
          AND service.account_id = NEW.account_id
          AND service.status <> 'deleted'
          AND NOT EXISTS (
              SELECT 1 FROM apps production
              WHERE production.account_id = service.account_id
                AND production.slug = service.preview_of_slug
                AND production.preview_of_slug IS NULL
                AND production.status <> 'deleted')
          AND (service.require_authn, service.public_auth_mode, service.public_auth_basic,
               service.public_auth_ip_allowlist, service.consumer_auth_mode, service.require_signed,
               service.egress_allowlist, service.platform_tenant_required, service.security_policy)
              IS DISTINCT FROM (NEW.require_authn, NEW.public_auth_mode, NEW.public_auth_basic,
               NEW.public_auth_ip_allowlist, NEW.consumer_auth_mode, NEW.require_signed,
               NEW.egress_allowlist, NEW.platform_tenant_required, NEW.security_policy)
        RETURNING service.id
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
        only_declared_routes, declared_routes, platform_tenant_required, security_policy ON apps
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
        OLD.declared_routes IS DISTINCT FROM NEW.declared_routes OR
        OLD.platform_tenant_required IS DISTINCT FROM NEW.platform_tenant_required OR
        OLD.security_policy IS DISTINCT FROM NEW.security_policy))
    EXECUTE FUNCTION sync_preview_access_gates();

-- Previews of an existing app take its values.
-- +goose StatementBegin
DO $backfill$
DECLARE
    changed uuid[];
    preview_id uuid;
BEGIN
    LOOP
        WITH batch AS (
            SELECT preview.id, parent.platform_tenant_required, parent.security_policy
            FROM apps preview
            JOIN apps parent ON parent.account_id = preview.account_id
                AND parent.slug = preview.preview_of_slug
                AND parent.preview_of_slug IS NULL
                AND parent.status <> 'deleted'
            WHERE preview.preview_of_slug IS NOT NULL
              AND preview.status <> 'deleted'
              AND (preview.platform_tenant_required, preview.security_policy)
                  IS DISTINCT FROM (parent.platform_tenant_required, parent.security_policy)
            LIMIT 500
        ), updated AS (
            UPDATE apps
            SET platform_tenant_required = batch.platform_tenant_required,
                security_policy = batch.security_policy
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

-- Services a PR adds take the values of the app the PR previews.
-- +goose StatementBegin
DO $backfill$
DECLARE
    changed uuid[];
    preview_id uuid;
BEGIN
    LOOP
        WITH batch AS (
            SELECT service.id, bound.platform_tenant_required, bound.security_policy
            FROM apps service
            JOIN pr_preview_sets preview_set ON preview_set.member_app_ids @> ARRAY[service.id::text]
            JOIN apps root ON root.id = preview_set.root_app_id
                AND root.id <> service.id
                AND root.account_id = service.account_id
            JOIN apps bound ON bound.account_id = root.account_id
                AND bound.slug = root.preview_of_slug
                AND bound.preview_of_slug IS NULL
                AND bound.status <> 'deleted'
            WHERE service.status <> 'deleted'
              AND service.preview_of_slug IS NOT NULL
              AND NOT EXISTS (
                SELECT 1 FROM apps production
                WHERE production.account_id = service.account_id
                  AND production.slug = service.preview_of_slug
                  AND production.preview_of_slug IS NULL
                  AND production.status <> 'deleted')
              AND (service.platform_tenant_required, service.security_policy)
                  IS DISTINCT FROM (bound.platform_tenant_required, bound.security_policy)
            LIMIT 500
        ), updated AS (
            UPDATE apps
            SET platform_tenant_required = batch.platform_tenant_required,
                security_policy = batch.security_policy
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
    -- A service the PR adds has no production app; its preview keeps the
    -- access and egress gates of the app the PR previews (the root of its
    -- PR environment), not that app's visibility or route contract.
    FOR preview_id IN
        UPDATE apps service
        SET require_authn = NEW.require_authn,
            public_auth_mode = NEW.public_auth_mode,
            public_auth_basic = NEW.public_auth_basic,
            public_auth_ip_allowlist = NEW.public_auth_ip_allowlist,
            consumer_auth_mode = NEW.consumer_auth_mode,
            require_signed = NEW.require_signed,
            egress_allowlist = NEW.egress_allowlist
        FROM pr_preview_sets preview_set
        JOIN apps root ON root.id = preview_set.root_app_id
        WHERE root.account_id = NEW.account_id
          AND root.preview_of_slug = NEW.slug
          AND preview_set.member_app_ids @> ARRAY[service.id::text]
          AND service.id <> root.id
          AND service.account_id = NEW.account_id
          AND service.status <> 'deleted'
          AND NOT EXISTS (
              SELECT 1 FROM apps production
              WHERE production.account_id = service.account_id
                AND production.slug = service.preview_of_slug
                AND production.preview_of_slug IS NULL
                AND production.status <> 'deleted')
          AND (service.require_authn, service.public_auth_mode, service.public_auth_basic,
               service.public_auth_ip_allowlist, service.consumer_auth_mode, service.require_signed,
               service.egress_allowlist)
              IS DISTINCT FROM (NEW.require_authn, NEW.public_auth_mode, NEW.public_auth_basic,
               NEW.public_auth_ip_allowlist, NEW.consumer_auth_mode, NEW.require_signed,
               NEW.egress_allowlist)
        RETURNING service.id
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

RESET lock_timeout;
