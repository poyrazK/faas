-- filename: 20261031104023311_preview_new_service_gates.sql
-- migration-compat: expand
--
-- ADR-20261031120000: a service a pull request adds has no production app of
-- its own, so its preview takes the access and egress gates of the app the
-- PR previews. Those gates now follow that app like a preview's own gates
-- do: the trigger also updates the PR environments' new services. Their
-- visibility and route contract stay their own; a new service was given the
-- previewed app's route contract, which refused its own routes, and the
-- backfill clears it. Each backfill batch commits on its own; every step is
-- re-runnable.
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

-- +goose StatementBegin
DO $backfill$
DECLARE
    changed uuid[];
    preview_id uuid;
BEGIN
    LOOP
        WITH batch AS (
            SELECT service.id, bound.require_authn, bound.public_auth_mode, bound.public_auth_basic,
                   bound.public_auth_ip_allowlist, bound.consumer_auth_mode, bound.require_signed,
                   bound.egress_allowlist
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
              AND ((service.require_authn, service.public_auth_mode, service.public_auth_basic,
                    service.public_auth_ip_allowlist, service.consumer_auth_mode, service.require_signed,
                    service.egress_allowlist)
                   IS DISTINCT FROM (bound.require_authn, bound.public_auth_mode, bound.public_auth_basic,
                    bound.public_auth_ip_allowlist, bound.consumer_auth_mode, bound.require_signed,
                    bound.egress_allowlist)
                   OR service.only_declared_routes
                   OR service.declared_routes IS DISTINCT FROM '[]'::jsonb)
            LIMIT 500
        ), updated AS (
            UPDATE apps
            SET require_authn = batch.require_authn,
                public_auth_mode = batch.public_auth_mode,
                public_auth_basic = batch.public_auth_basic,
                public_auth_ip_allowlist = batch.public_auth_ip_allowlist,
                consumer_auth_mode = batch.consumer_auth_mode,
                require_signed = batch.require_signed,
                egress_allowlist = batch.egress_allowlist,
                only_declared_routes = false,
                declared_routes = '[]'::jsonb
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
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

RESET lock_timeout;
