-- filename: 20260930203005001_exclusive_work_trigger_bindings.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE exclusive_work_trigger_bindings (
  source text NOT NULL CHECK (source IN ('cron', 'inbound_webhook')),
  trigger_id uuid NOT NULL,
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  policy_id uuid NOT NULL,
  policy_name text NOT NULL CHECK (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
  platform_tenant_id uuid,
  business_key jsonb NOT NULL CHECK (jsonb_typeof(business_key) IN ('string', 'number', 'boolean')),
  equivalence_key text NOT NULL DEFAULT '' CHECK (octet_length(equivalence_key) <= 256),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (source, trigger_id),
  FOREIGN KEY (policy_id, account_id) REFERENCES exclusive_work_policies(id, account_id),
  FOREIGN KEY (account_id, platform_tenant_id) REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE
);

CREATE INDEX exclusive_work_trigger_bindings_policy_idx
  ON exclusive_work_trigger_bindings (account_id, policy_name);

CREATE FUNCTION delete_exclusive_cron_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM exclusive_work_trigger_bindings WHERE source = 'cron' AND trigger_id = OLD.id;
  RETURN OLD;
END;
$$;
CREATE TRIGGER crons_delete_exclusive_binding
  AFTER DELETE ON crons FOR EACH ROW EXECUTE FUNCTION delete_exclusive_cron_binding();

CREATE FUNCTION delete_exclusive_webhook_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  DELETE FROM exclusive_work_trigger_bindings WHERE source = 'inbound_webhook' AND trigger_id = OLD.id;
  RETURN OLD;
END;
$$;
CREATE TRIGGER inbound_webhooks_delete_exclusive_binding
  AFTER DELETE ON inbound_webhook_endpoints FOR EACH ROW EXECUTE FUNCTION delete_exclusive_webhook_binding();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER inbound_webhooks_delete_exclusive_binding ON inbound_webhook_endpoints;
DROP FUNCTION delete_exclusive_webhook_binding();
DROP TRIGGER crons_delete_exclusive_binding ON crons;
DROP FUNCTION delete_exclusive_cron_binding();
DROP TABLE exclusive_work_trigger_bindings;
-- +goose StatementEnd
