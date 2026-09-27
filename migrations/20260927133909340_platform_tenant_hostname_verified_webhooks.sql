-- filename: 20260927133909340_platform_tenant_hostname_verified_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- A tenant-owned hostname becoming DNS-verified is an actionable lifecycle
-- event for a platform. Enqueue into the shared delivery ledger in the same
-- transaction as the verification transition, but only after the surface is
-- explicitly linked to a platform tenant.
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
        AND cardinality(event_filter) BETWEEN 1 AND 2
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified'
        ]::text[]
        AND (cardinality(event_filter) = 1 OR event_filter[1] <> event_filter[2]))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_platform_tenant_hostname_verified_uniq
  ON app_webhook_deliveries (webhook_id, (payload->>'hostname_id'))
  WHERE event = 'platform_tenant.hostname.verified';

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_hostname_verified_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  event_payload jsonb;
  v_tenant_id uuid;
  v_tenant_external_ref text;
  v_surface_name text;
  v_app_id uuid;
  v_account_id uuid;
BEGIN
  IF OLD.verified_at IS NOT NULL OR NEW.verified_at IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT s.platform_tenant_id, t.external_ref, s.name::text, s.app_id, s.account_id
    INTO v_tenant_id, v_tenant_external_ref, v_surface_name, v_app_id, v_account_id
    FROM tenant_surfaces s
    LEFT JOIN platform_tenants t
      ON t.account_id = s.account_id AND t.id = s.platform_tenant_id
   WHERE s.id = NEW.surface_id;

  -- Unlinked surfaces stay on their existing app-scoped integration path.
  IF v_tenant_id IS NULL OR v_tenant_external_ref IS NULL THEN
    RETURN NEW;
  END IF;

  event_payload := jsonb_build_object(
    'platform_tenant_id', v_tenant_id,
    'external_ref', v_tenant_external_ref,
    'surface_id', NEW.surface_id,
    'surface_name', v_surface_name,
    'app_id', v_app_id,
    'hostname_id', NEW.id,
    'hostname', NEW.hostname,
    'verified_at', NEW.verified_at
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, h.account_id, 'platform_tenant.hostname.verified', event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = v_tenant_id
     AND h.account_id = v_account_id
     AND h.enabled
     AND 'platform_tenant.hostname.verified' = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_hostname_verified_webhooks ON tenant_hostnames;
CREATE TRIGGER platform_tenant_hostname_verified_webhooks
  AFTER UPDATE OF verified_at ON tenant_hostnames
  FOR EACH ROW
  WHEN (OLD.verified_at IS NULL AND NEW.verified_at IS NOT NULL)
  EXECUTE FUNCTION enqueue_platform_tenant_hostname_verified_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: removing the event source would strand configured receivers
-- and make already-enqueued webhook deliveries impossible to explain.
SELECT 1;
-- +goose StatementEnd
