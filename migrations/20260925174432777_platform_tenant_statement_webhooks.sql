-- filename: 20260925174432777_platform_tenant_statement_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- A tenant webhook is account-owned but has a platform-tenant event source.
-- This makes cross-app billing events a single durable delivery rather than
-- an arbitrary per-app fan-out.
ALTER TABLE app_webhooks
  ADD COLUMN IF NOT EXISTS platform_tenant_id uuid;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_catalog.pg_constraint
     WHERE conname = 'app_webhooks_platform_tenant_fkey'
       AND conrelid = 'app_webhooks'::regclass
  ) THEN
    ALTER TABLE app_webhooks
      ADD CONSTRAINT app_webhooks_platform_tenant_fkey
      FOREIGN KEY (account_id, platform_tenant_id)
      REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE;
  END IF;
END$$;

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
        AND cardinality(event_filter) = 1
        AND event_filter = ARRAY['platform_tenant.statement.finalized']::text[])
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

CREATE UNIQUE INDEX IF NOT EXISTS app_webhooks_platform_tenant_target_uniq
  ON app_webhooks (platform_tenant_id, target_url)
  WHERE scope = 'platform_tenant';

-- Account-level tenant billing deliveries have no single source app. Keep the
-- app FK for app and release events, but permit NULL for a tenant event.
ALTER TABLE app_webhook_deliveries ALTER COLUMN app_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_platform_tenant_statement_uniq
  ON app_webhook_deliveries
    (webhook_id, (payload->>'statement_id'), (payload->>'revision'))
  WHERE event = 'platform_tenant.statement.finalized';

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_statement_finalized_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  event_payload jsonb;
  tenant_external_ref text;
BEGIN
  IF OLD.status = 'finalized' OR NEW.status <> 'finalized' THEN
    RETURN NEW;
  END IF;

  SELECT external_ref INTO tenant_external_ref
    FROM platform_tenants
   WHERE account_id = NEW.account_id AND id = NEW.platform_tenant_id;

  event_payload := jsonb_build_object(
    'platform_tenant_id', NEW.platform_tenant_id,
    'external_ref', tenant_external_ref,
    'statement_id', NEW.id,
    'revision', NEW.revision,
    'status', NEW.status,
    'period_start', NEW.period_start,
    'period_end', NEW.period_end,
    'currency', NEW.currency,
    'billable_units', NEW.billable_units,
    'unpriced_units', NEW.unpriced_units,
    'amount_millicents', NEW.amount_millicents,
    'priced', NEW.unpriced_units = 0 AND NEW.currency <> '',
    'lines', NEW.lines,
    'as_of', NEW.as_of,
    'finalized_at', NEW.finalized_at
  );

  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, NEW.account_id, 'platform_tenant.statement.finalized', event_payload
    FROM app_webhooks h
   WHERE h.scope = 'platform_tenant'
     AND h.platform_tenant_id = NEW.platform_tenant_id
     AND h.account_id = NEW.account_id
     AND h.enabled
     AND 'platform_tenant.statement.finalized' = ANY(h.event_filter)
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_statement_finalized_webhooks ON platform_tenant_statements;
CREATE TRIGGER platform_tenant_statement_finalized_webhooks
  AFTER UPDATE OF status ON platform_tenant_statements
  FOR EACH ROW
  WHEN (OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION enqueue_platform_tenant_statement_finalized_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: removing tenant subscriptions or their delivery source would
-- strand configured integrations and their durable accounting events.
SELECT 1;
-- +goose StatementEnd
