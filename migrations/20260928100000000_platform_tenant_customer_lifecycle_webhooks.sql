-- filename: 20260928100000000_platform_tenant_customer_lifecycle_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- Keep a platform's own customer registry synchronized with the tenant's
-- app-local identities. These records enter the shared durable webhook ledger
-- in the same transaction as the link or revocation, and expose no credentials.
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
        AND cardinality(event_filter) BETWEEN 1 AND 6
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified',
          'platform_tenant.surface.certificate.changed',
          'platform_tenant.surface.deployment.changed',
          'platform_tenant.customer.linked',
          'platform_tenant.customer.offboarded'
        ]::text[]
        AND (cardinality(event_filter) < 2 OR event_filter[1] <> event_filter[2])
        AND (cardinality(event_filter) < 3 OR
             (event_filter[1] <> event_filter[3] AND event_filter[2] <> event_filter[3]))
        AND (cardinality(event_filter) < 4 OR
             (event_filter[1] <> event_filter[4] AND event_filter[2] <> event_filter[4]
              AND event_filter[3] <> event_filter[4]))
        AND (cardinality(event_filter) < 5 OR
             (event_filter[1] <> event_filter[5] AND event_filter[2] <> event_filter[5]
              AND event_filter[3] <> event_filter[5] AND event_filter[4] <> event_filter[5]))
        AND (cardinality(event_filter) < 6 OR
             (event_filter[1] <> event_filter[6] AND event_filter[2] <> event_filter[6]
              AND event_filter[3] <> event_filter[6] AND event_filter[4] <> event_filter[6]
              AND event_filter[5] <> event_filter[6])))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

-- A given identity can be linked and later offboarded once. Retries of either
-- source transition cannot create another delivery for that receiver.
CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_platform_tenant_customer_lifecycle_uniq
  ON app_webhook_deliveries (webhook_id, event, (payload->>'consumer_id'))
  WHERE event IN ('platform_tenant.customer.linked', 'platform_tenant.customer.offboarded');

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_customer_lifecycle_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  event_name text;
  tenant_external_ref text;
  event_payload jsonb;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.platform_tenant_id IS NULL OR NEW.status <> 'active' THEN
      RETURN NEW;
    END IF;
    event_name := 'platform_tenant.customer.linked';
  ELSIF OLD.platform_tenant_id IS NULL AND NEW.platform_tenant_id IS NOT NULL
        AND OLD.status = 'active' AND NEW.status = 'active' THEN
    event_name := 'platform_tenant.customer.linked';
  ELSIF OLD.platform_tenant_id = NEW.platform_tenant_id
        AND OLD.platform_tenant_id IS NOT NULL
        AND OLD.status = 'active' AND NEW.status = 'revoked' THEN
    event_name := 'platform_tenant.customer.offboarded';
  ELSE
    RETURN NEW;
  END IF;

  SELECT external_ref INTO tenant_external_ref
    FROM platform_tenants
   WHERE account_id = NEW.account_id AND id = NEW.platform_tenant_id;
  IF tenant_external_ref IS NULL THEN
    RETURN NEW;
  END IF;

  event_payload := jsonb_build_object(
    'platform_tenant_id', NEW.platform_tenant_id,
    'external_ref', tenant_external_ref,
    'consumer_id', NEW.id,
    'app_id', NEW.app_id,
    'customer_external_ref', NEW.external_ref,
    'customer_name', NEW.name,
    'customer_status', NEW.status,
    'changed_at', statement_timestamp()
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, h.account_id, event_name, event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = NEW.platform_tenant_id
     AND h.account_id = NEW.account_id
     AND h.enabled
     AND event_name = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_customer_lifecycle_webhooks ON api_consumers;
CREATE TRIGGER platform_tenant_customer_lifecycle_webhooks
  AFTER INSERT OR UPDATE OF platform_tenant_id, status ON api_consumers
  FOR EACH ROW
  EXECUTE FUNCTION enqueue_platform_tenant_customer_lifecycle_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: existing receiver filters and queued customer events rely on
-- this event vocabulary and must remain deliverable after rollback attempts.
SELECT 1;
-- +goose StatementEnd
