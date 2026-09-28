-- filename: 20260928123157001_org_activity_env_domain_backfill.sql

-- +goose Up
-- +goose StatementBegin

-- Import only customer-safe display facts from historical environment and
-- custom-domain events. Do not copy the raw payload: legacy and future audit
-- emitters may include fields that are not appropriate for this projection.
--
-- Current handlers write both events and org_activity. The first live
-- activity (including an outbox item that has not yet been delivered) for
-- each kind is the cutover. Events at or after it are already covered by the
-- live projection and must not be imported again.
WITH live_activity AS (
    SELECT kind, occurred_at AS first_seen_at
      FROM org_activity
     WHERE kind IN ('env.set', 'env.deleted', 'domain.added', 'domain.removed')
    UNION ALL
    SELECT COALESCE(activity->>'Kind', activity->>'kind') AS kind,
           created_at AS first_seen_at
      FROM org_activity_outbox
     WHERE COALESCE(activity->>'Kind', activity->>'kind')
           IN ('env.set', 'env.deleted', 'domain.added', 'domain.removed')
), cutover AS (
    SELECT kind, min(first_seen_at) AS first_seen_at
      FROM live_activity
     GROUP BY kind
), legacy AS (
    SELECT
        e.id,
        e.at,
        e.actor,
        e.actor_account_id,
        e.subject,
        e.kind,
        e.data,
        CASE
            WHEN e.data->>'app_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (e.data->>'app_id')::uuid
        END AS app_id,
        CASE
            WHEN e.data->>'actor_user_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
            THEN (e.data->>'actor_user_id')::uuid
        END AS payload_actor_id,
        COALESCE(NULLIF(e.data->>'scope', ''), 'default') AS scope,
        e.data->>'name' AS env_name,
        e.data->>'domain' AS domain,
        NULLIF(e.data->>'environment', '') AS environment
    FROM events e
    WHERE e.kind IN ('env.set', 'env.deleted', 'domain.added', 'domain.removed')
      AND e.data->>'app_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
), candidates AS (
    SELECT
        l.id AS event_id,
        l.at AS occurred_at,
        l.kind,
        a.org_id,
        a.id AS app_id,
        l.scope,
        l.env_name,
        l.domain,
        l.environment,
        actor.id AS resolved_actor_id,
        CASE
            WHEN l.actor LIKE 'github:%' OR l.data->>'actor_via' = 'github' THEN 'github'
            WHEN l.actor LIKE 'api:%' OR l.data->>'actor_via' = 'api' THEN 'api_key'
            WHEN l.data->>'actor_via' = 'operator' THEN 'operator'
            WHEN actor.id IS NOT NULL THEN 'user'
            ELSE 'system'
        END AS actor_type,
        CASE
            WHEN l.actor LIKE 'github:%' OR l.data->>'actor_via' = 'github' THEN 'GitHub Actions'
            WHEN l.actor LIKE 'api:%' OR l.data->>'actor_via' = 'api' THEN 'API key'
            WHEN l.data->>'actor_via' = 'operator' THEN COALESCE(actor.email::text, 'Gregale operator')
            WHEN actor.id IS NOT NULL THEN actor.email::text
            ELSE 'Unknown (legacy event)'
        END AS actor_label
    FROM legacy l
    JOIN apps a ON a.id = l.app_id
    LEFT JOIN cutover c ON c.kind = l.kind
    LEFT JOIN accounts actor
      ON actor.id = COALESCE(l.actor_account_id, l.payload_actor_id, l.subject)
    WHERE a.org_id IS NOT NULL
      AND (c.first_seen_at IS NULL OR l.at < c.first_seen_at)
      AND (
          (l.kind IN ('env.set', 'env.deleted')
           AND l.env_name ~ '^[A-Z][A-Z0-9_]{0,127}$'
           AND (l.scope = 'default' OR l.scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'))
          OR
          (l.kind IN ('domain.added', 'domain.removed')
           AND length(l.domain) <= 253
           AND l.domain ~ '^([*][.])?[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?([.][A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$'
           AND (l.environment IS NULL OR l.environment = 'default'
                OR l.environment ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'))
      )
)
INSERT INTO org_activity (
    org_id, occurred_at, kind, actor_type, actor_account_id, actor_label,
    resource_type, resource_id, resource_label, app_id, data,
    source_type, source_id
)
SELECT
    org_id,
    occurred_at,
    kind,
    actor_type,
    CASE WHEN actor_type IN ('user', 'operator') THEN resolved_actor_id END,
    actor_label,
    CASE WHEN kind IN ('env.set', 'env.deleted') THEN 'environment_variable' ELSE 'domain' END,
    CASE
        WHEN kind IN ('env.set', 'env.deleted') THEN scope || ':' || env_name
        ELSE domain
    END,
    CASE WHEN kind IN ('env.set', 'env.deleted') THEN env_name ELSE domain END,
    app_id,
    CASE
        WHEN kind IN ('env.set', 'env.deleted') THEN jsonb_build_object('scope', scope)
        WHEN kind = 'domain.added' THEN jsonb_strip_nulls(jsonb_build_object('environment', environment))
        ELSE '{}'::jsonb
    END,
    'legacy.events',
    event_id::text
FROM candidates
ON CONFLICT (org_id, source_type, source_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- Forward-only data repair. Imported activity shares the append-only
-- projection with live events and is intentionally not deleted on rollback.
