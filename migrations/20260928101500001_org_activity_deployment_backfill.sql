-- filename: 20260928101500001_org_activity_deployment_backfill.sql

-- +goose Up
-- +goose StatementBegin

-- Seed the organization timeline from the legacy deployment intent events.
-- These are curated facts, not a raw events union: only deployment intent,
-- the surviving app's current display label, and safe source/actor classes
-- are copied. Events whose app or deployment identifiers cannot be resolved
-- are skipped rather than guessed.
--
-- The source key matches the live deployment.requested producer
-- (deployment id), so deployments already projected by current apid code and
-- imports from multiple legacy event kinds collapse to one timeline row.
-- Actor display values were not captured by these old rows; when the source
-- identifies an account, its current email is snapshotted as the best
-- available historical label. Otherwise attribution is explicit as unknown.
WITH legacy AS (
    SELECT
        e.id,
        e.at,
        e.actor,
        e.actor_account_id,
        e.kind,
        e.data,
        CASE
            WHEN e.data->>'app_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (e.data->>'app_id')::uuid
        END AS payload_app_id,
        CASE
            WHEN e.data->>'deployment_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (e.data->>'deployment_id')::uuid
        END AS deployment_id,
        CASE
            WHEN e.data->>'actor_user_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (e.data->>'actor_user_id')::uuid
            WHEN split_part(e.actor, ':', 2) ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN split_part(e.actor, ':', 2)::uuid
        END AS payload_actor_id
    FROM events e
    WHERE e.kind IN ('app.deployed', 'deploy.source_ref', 'deploy.local_tarball')
      AND e.data->>'app_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      AND e.data->>'deployment_id' ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
), candidates AS (
    SELECT
        l.id AS event_id,
        a.org_id,
        l.at AS occurred_at,
        a.id AS app_id,
        a.slug AS app_slug,
        l.deployment_id,
        l.kind AS legacy_kind,
        CASE l.kind
            WHEN 'deploy.source_ref' THEN 'source_ref'
            WHEN 'deploy.local_tarball' THEN 'local_tarball'
            ELSE 'image'
        END AS source,
        actor.id AS resolved_actor_id,
        CASE
            WHEN l.actor LIKE 'github:%' OR l.data->>'actor_via' = 'github' THEN 'github'
            WHEN l.data->>'actor_via' = 'api' THEN 'api_key'
            WHEN l.data->>'actor_via' = 'operator' THEN 'operator'
            WHEN actor.id IS NOT NULL THEN 'user'
            ELSE 'system'
        END AS actor_type,
        CASE
            WHEN l.actor LIKE 'github:%' OR l.data->>'actor_via' = 'github' THEN 'GitHub Actions'
            WHEN l.data->>'actor_via' = 'api' THEN 'API key'
            WHEN l.data->>'actor_via' = 'operator' THEN COALESCE(actor.email::text, 'Gregale operator')
            WHEN actor.id IS NOT NULL THEN actor.email::text
            ELSE 'Unknown (legacy event)'
        END AS actor_label
    FROM legacy l
    JOIN apps a ON a.id = l.payload_app_id
    LEFT JOIN accounts actor
      ON actor.id = COALESCE(l.actor_account_id, l.payload_actor_id)
    WHERE a.org_id IS NOT NULL
), one_per_deployment AS (
    SELECT DISTINCT ON (org_id, deployment_id)
        org_id,
        occurred_at,
        app_id,
        app_slug,
        deployment_id,
        event_id,
        legacy_kind,
        source,
        resolved_actor_id,
        actor_type,
        actor_label
    FROM candidates
    ORDER BY org_id, deployment_id, occurred_at, legacy_kind, app_id, event_id
)
INSERT INTO org_activity (
    org_id, occurred_at, kind, actor_type, actor_account_id, actor_label,
    resource_type, resource_id, resource_label, app_id, deployment_id,
    data, source_type, source_id
)
SELECT
    org_id,
    occurred_at,
    'deploy.requested',
    actor_type,
    CASE WHEN actor_type IN ('user', 'operator') THEN resolved_actor_id END,
    actor_label,
    'app',
    app_id::text,
    app_slug,
    app_id,
    deployment_id,
    jsonb_build_object('phase', 'requested', 'source', source),
    'deployment.requested',
    deployment_id::text
FROM one_per_deployment
ON CONFLICT (org_id, source_type, source_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- Forward-only data repair. Imported rows use the same stable source key as
-- live activity, so deleting by source key could erase a row subsequently
-- refreshed by a live producer.
