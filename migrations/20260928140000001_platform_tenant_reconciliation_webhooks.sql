-- filename: 20260928140000001_platform_tenant_reconciliation_webhooks.sql

-- +goose Up
-- +goose StatementBegin
-- Notify a platform after a successful reconciliation commit. The receipt
-- row is the source of truth, so delivery is enqueued in the same transaction
-- and contains only a receipt pointer plus a bounded summary.
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
        AND cardinality(event_filter) BETWEEN 1 AND 7
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified',
          'platform_tenant.surface.certificate.changed',
          'platform_tenant.surface.deployment.changed',
          'platform_tenant.customer.linked',
          'platform_tenant.customer.offboarded',
          'platform_tenant.reconciliation.applied'
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
              AND event_filter[5] <> event_filter[6]))
        AND (cardinality(event_filter) < 7 OR
             (event_filter[1] <> event_filter[7] AND event_filter[2] <> event_filter[7]
              AND event_filter[3] <> event_filter[7] AND event_filter[4] <> event_filter[7]
              AND event_filter[5] <> event_filter[7] AND event_filter[6] <> event_filter[7])))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

CREATE UNIQUE INDEX IF NOT EXISTS app_webhook_deliveries_reconciliation_applied_uniq
  ON app_webhook_deliveries (webhook_id, (payload->>'receipt_id'))
  WHERE event = 'platform_tenant.reconciliation.applied';

CREATE OR REPLACE FUNCTION enqueue_platform_tenant_reconciliation_applied_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO app_webhook_deliveries
      (webhook_id, app_id, account_id, event, payload)
  SELECT h.id, NULL, h.account_id, 'platform_tenant.reconciliation.applied',
         jsonb_build_object(
           'platform_tenant_id', t.id,
           'external_ref', t.external_ref,
           'receipt_id', NEW.receipt_id,
           'plan_hash', NEW.plan_hash,
           'applied_at', NEW.applied_at,
           'change_count', jsonb_array_length(NEW.changes)
         )
    FROM platform_tenants t
    JOIN app_webhooks h
      ON h.account_id = t.account_id
     AND h.platform_tenant_id = t.id
     AND h.scope = 'platform_tenant'
     AND h.enabled
     AND 'platform_tenant.reconciliation.applied' = ANY(h.event_filter)
   WHERE t.account_id = NEW.account_id
     AND t.id = NEW.tenant_id
  ON CONFLICT DO NOTHING;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS platform_tenant_reconciliation_applied_webhooks ON platform_tenant_reconciliation_receipts;
CREATE TRIGGER platform_tenant_reconciliation_applied_webhooks
  AFTER INSERT ON platform_tenant_reconciliation_receipts
  FOR EACH ROW
  EXECUTE FUNCTION enqueue_platform_tenant_reconciliation_applied_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: queued reconciliation notifications must remain deliverable
-- and subscribed event filters depend on this event vocabulary.
SELECT 1;
-- +goose StatementEnd
