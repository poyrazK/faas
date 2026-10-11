-- filename: 20261003140000001_operation_webhook_effects.sql
-- +goose Up
-- +goose StatementBegin
-- Effect id is also the stable delivery id. Retain receiver/type identity after
-- webhook deletion or delivery retention; deliberately no FK on webhook_id.
ALTER TABLE exclusive_work_effects
 ADD COLUMN IF NOT EXISTS webhook_id uuid,
 ADD COLUMN IF NOT EXISTS event_type text;
ALTER TABLE exclusive_work_effects DROP CONSTRAINT IF EXISTS exclusive_work_effects_adapter_check;
ALTER TABLE exclusive_work_effects ADD CONSTRAINT exclusive_work_effects_adapter_check CHECK (
 (webhook_id IS NULL AND event_type IS NULL) OR
 (webhook_id IS NOT NULL AND event_type IS NOT NULL AND octet_length(event_type) BETWEEN 1 AND 256
  AND event_type ~ '^[a-z][a-z0-9_.-]*$')
) NOT VALID;
ALTER TABLE exclusive_work_effects VALIDATE CONSTRAINT exclusive_work_effects_adapter_check;

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
        AND cardinality(event_filter) BETWEEN 1 AND 8
        AND array_lower(event_filter, 1) = 1
        AND array_position(event_filter, NULL::text) IS NULL
        AND event_filter <@ ARRAY[
          'platform_tenant.statement.finalized',
          'platform_tenant.hostname.verified',
          'platform_tenant.surface.certificate.changed',
          'platform_tenant.surface.deployment.changed',
          'platform_tenant.customer.linked',
          'platform_tenant.customer.offboarded',
          'platform_tenant.reconciliation.applied', 'operation.effect'
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
              AND event_filter[5] <> event_filter[7] AND event_filter[6] <> event_filter[7]))
        AND (cardinality(event_filter) < 8 OR
             (event_filter[1] <> event_filter[8] AND event_filter[2] <> event_filter[8]
              AND event_filter[3] <> event_filter[8] AND event_filter[4] <> event_filter[8]
              AND event_filter[5] <> event_filter[8] AND event_filter[6] <> event_filter[8]
              AND event_filter[7] <> event_filter[8])))
  ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

-- +goose StatementEnd
-- +goose Down
-- Forward-only: committed effect and delivery identities are durable receipts.
SELECT 1;
