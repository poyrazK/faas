-- +goose Up
-- Reconcile migrated personal organizations with their owning account and
-- attach legacy tenant-root rows to that organization. Account billing remains
-- the write authority while org-scoped authorization reads the synchronized
-- projection.
-- +goose StatementBegin

UPDATE orgs o
   SET plan = a.plan,
       status = a.status,
       provider_customer_id = a.provider_customer_id,
       stripe_subscription_item = a.stripe_subscription_item,
       updated_at = now()
  FROM accounts a
 WHERE o.personal_org = true
   AND o.personal_owner_account_id = a.id
   AND (o.plan IS DISTINCT FROM a.plan
        OR o.status IS DISTINCT FROM a.status
        OR o.provider_customer_id IS DISTINCT FROM a.provider_customer_id
        OR o.stripe_subscription_item IS DISTINCT FROM a.stripe_subscription_item);

DO $$
DECLARE
    tenant_table text;
BEGIN
    FOREACH tenant_table IN ARRAY ARRAY[
        'apps', 'projects', 'custom_domains', 'instances',
        'usage_minutes', 'usage_daily', 'invoices',
        'stripe_push_dedupe', 'paddle_overage_dedupe',
        'app_secrets', 'app_envs', 'alert_rules',
        'recent_build_claims', 'builder_usage', 'crons',
        'invocations', 'github_installations', 'gdpr_requests'
    ] LOOP
        EXECUTE format(
            'UPDATE %I t SET org_id = o.id FROM orgs o WHERE t.org_id IS NULL AND o.personal_org = true AND o.personal_owner_account_id = t.account_id',
            tenant_table);
    END LOOP;
END$$;

-- api_keys were made NOT NULL and backfilled by migration 00134. Retain an
-- idempotent repair for databases that partially applied that transition.
UPDATE api_keys k
   SET org_id = o.id
  FROM orgs o
 WHERE k.org_id IS NULL
   AND o.personal_org = true
   AND o.personal_owner_account_id = k.account_id;

-- +goose StatementEnd

-- +goose Down
-- Forward-only data reconciliation. Reversing it would reintroduce ambiguous
-- tenant ownership and stale billing identifiers.
