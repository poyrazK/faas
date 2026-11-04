-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS scaling_revision bigint NOT NULL DEFAULT 1;

-- Every writer participates, including legacy PATCH and environment promotion.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_app_scaling_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF ROW(NEW.ram_mb, NEW.cpu_millicores, NEW.max_concurrency, NEW.min_instances)
     IS DISTINCT FROM ROW(OLD.ram_mb, OLD.cpu_millicores, OLD.max_concurrency, OLD.min_instances) THEN
    NEW.scaling_revision := OLD.scaling_revision + 1;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS apps_scaling_revision ON apps;
CREATE TRIGGER apps_scaling_revision BEFORE UPDATE ON apps
FOR EACH ROW EXECUTE FUNCTION bump_app_scaling_revision();

CREATE TABLE IF NOT EXISTS app_scaling_receipts (
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  scope text NOT NULL,
  request_id uuid NOT NULL,
  request_hash bytea NOT NULL,
  response jsonb NOT NULL,
  PRIMARY KEY (app_id, scope, request_id)
);

-- +goose Down
-- Expand-only rollback: keep committed request identities and revision guards.
SELECT 1;
