-- filename: 20260928024555300_platform_tenant_surface_deployment_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- A platform needs a durable signal when a deployment for one of its
-- explicitly linked customer surfaces reaches a terminal outcome. Keep the
-- event in the shared ledger and in the same transaction as the status
-- transition. Do not include app/deployment IDs, source metadata, logs, or
-- raw deployment errors in the tenant-facing payload.
ALTER TABLE app_webhooks
  DROP CONSTRAINT IF EXISTS app_webhooks_scope_chk;
ALTER TABLE app_webhooks
  ADD CONSTRAINT app_webhooks_scope_chk CHECK (
    (scope = 'app' AND app_id IS NOT NULL AND platform_tenant_id IS NULL)
    OR (scope = 'account' AND app_id IS NULL AND platform_tenant_id IS NULL
        AND cardinality(event_filter) BETWEEN 1 AND 4
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'deployment.live', 'deployment.failed',
          'rollout.completed', 'rollout.aborted'
        ]::text[])
    OR (scope = 'platform_tenant' AND app_id IS NULL AND platform_tenant_id IS NOT NULL
        AND cardinality(event_filter) BETWEEN 1 AND 4
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified',
          'platform_tenant.surface.certificate.changed',
          'platform_tenant.surface.deployment.changed'
        ]::text[]
        AND (cardinality(event_filter) < 2 OR event_filter[1] <> event_filter[2])
        AND (cardinality(event_filter) < 3 OR
             (event_filter[1] <> event_filter[3] AND event_filter[2] <> event_filter[3]))
        AND (cardinality(event_filter) < 4 OR
             (event_filter[1] <> event_filter[4] AND event_filter[2] <> event_filter[4]
              AND event_filter[3] <> event_filter[4])))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

-- A retry of the same status write cannot create a second logical event for
-- the same surface/revision/transition timestamp.
CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_platform_tenant_deployment_changed_uniq
  ON app_webhook_deliveries
    (webhook_id, (payload->>'surface_id'), (payload->>'revision'),
     (payload->>'deployment_status'), (payload->>'changed_at'))
  WHERE event = 'platform_tenant.surface.deployment.changed';

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_surface_deployment_changed_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status NOT IN ('live', 'failed') OR NEW.revision <= 0 THEN
    RETURN NEW;
  END IF;

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, h.account_id, 'platform_tenant.surface.deployment.changed',
         jsonb_build_object(
           'platform_tenant_id', t.id,
           'external_ref', t.external_ref,
           'surface_id', s.id,
           'surface_name', s.name::text,
           'revision', NEW.revision,
           'deployment_status', NEW.status,
           'started_at', NEW.created_at,
           'changed_at', statement_timestamp()
         )
    FROM tenant_surfaces s
    JOIN platform_tenants t
      ON t.account_id = s.account_id AND t.id = s.platform_tenant_id
    JOIN app_webhooks h
      ON h.account_id = s.account_id
     AND h.platform_tenant_id = t.id
     AND h.scope = 'platform_tenant'
     AND h.enabled
     AND 'platform_tenant.surface.deployment.changed' = ANY(h.event_filter)
   WHERE s.app_id = NEW.app_id
     AND s.platform_tenant_id IS NOT NULL
     AND s.status <> 'deleted'
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_surface_deployment_changed_webhooks ON deployments;
CREATE TRIGGER platform_tenant_surface_deployment_changed_webhooks
  AFTER UPDATE OF status ON deployments
  FOR EACH ROW
  WHEN (OLD.status IS DISTINCT FROM NEW.status AND NEW.status IN ('live', 'failed'))
  EXECUTE FUNCTION enqueue_platform_tenant_surface_deployment_changed_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: removing the event source would strand configured receivers
-- and make already-enqueued webhook deliveries impossible to explain.
SELECT 1;
-- +goose StatementEnd
