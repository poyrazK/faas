-- filename: 20260927221351822_platform_tenant_surface_certificate_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- Platform-owned onboarding needs the next durable milestone after DNS
-- verification: certificate state changes for a surface explicitly linked
-- to a platform tenant. Keep this event in the shared delivery ledger and in
-- the same transaction as the state transition. Do not expose provider error
-- text or certificate/key material in the event payload.
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
        AND cardinality(event_filter) BETWEEN 1 AND 3
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified',
          'platform_tenant.surface.certificate.changed'
        ]::text[]
        AND (cardinality(event_filter) < 2 OR event_filter[1] <> event_filter[2])
        AND (cardinality(event_filter) < 3 OR
             (event_filter[1] <> event_filter[3] AND event_filter[2] <> event_filter[3])))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_surface_certificate_changed_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  event_payload jsonb;
  v_tenant_external_ref text;
BEGIN
  IF OLD.cert_state IS NOT DISTINCT FROM NEW.cert_state THEN
    RETURN NEW;
  END IF;

  IF NEW.platform_tenant_id IS NULL THEN
    RETURN NEW;
  END IF;

  SELECT external_ref INTO v_tenant_external_ref
    FROM platform_tenants
   WHERE account_id = NEW.account_id AND id = NEW.platform_tenant_id;

  -- Unlinked surfaces remain on the existing app-scoped integration path.
  IF v_tenant_external_ref IS NULL THEN
    RETURN NEW;
  END IF;

  event_payload := jsonb_build_object(
    'platform_tenant_id', NEW.platform_tenant_id,
    'external_ref', v_tenant_external_ref,
    'surface_id', NEW.id,
    'surface_name', NEW.name::text,
    'app_id', NEW.app_id,
    'cert_state', NEW.cert_state,
    'cert_not_after', NEW.cert_not_after,
    'changed_at', statement_timestamp()
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, h.account_id, 'platform_tenant.surface.certificate.changed', event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = NEW.platform_tenant_id
     AND h.account_id = NEW.account_id
     AND h.enabled
     AND 'platform_tenant.surface.certificate.changed' = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_surface_certificate_changed_webhooks ON tenant_surfaces;
CREATE TRIGGER platform_tenant_surface_certificate_changed_webhooks
  AFTER UPDATE OF cert_state ON tenant_surfaces
  FOR EACH ROW
  WHEN (OLD.cert_state IS DISTINCT FROM NEW.cert_state)
  EXECUTE FUNCTION enqueue_platform_tenant_surface_certificate_changed_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: removing this event source would strand configured receivers
-- and make already-enqueued webhook deliveries impossible to explain.
SELECT 1;
-- +goose StatementEnd
