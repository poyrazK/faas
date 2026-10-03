-- +goose Up
-- +goose StatementBegin
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS platform_tenant_id uuid;
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_platform_tenant_fk;
ALTER TABLE invocations ADD CONSTRAINT invocation_platform_tenant_fk
  FOREIGN KEY (account_id, platform_tenant_id)
  REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE;
ALTER TABLE invocations DROP CONSTRAINT IF EXISTS invocation_platform_tenant_source;
ALTER TABLE invocations ADD CONSTRAINT invocation_platform_tenant_source CHECK (
  platform_tenant_id IS NULL OR source IN ('async_invoke', 'replay')
);
CREATE INDEX IF NOT EXISTS invocations_platform_tenant_idx
  ON invocations(account_id, platform_tenant_id, created_at DESC)
  WHERE platform_tenant_id IS NOT NULL;

-- The tenant lock serializes admission with suspension. An admitted dispatch
-- may finish; suspension holds subsequent claims without consuming attempts.
CREATE OR REPLACE FUNCTION guard_invocation_platform_tenant() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE tenant_status text;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF NEW.platform_tenant_id IS DISTINCT FROM OLD.platform_tenant_id OR
       (OLD.platform_tenant_id IS NOT NULL AND
        (NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.account_id IS DISTINCT FROM OLD.account_id)) THEN
      RAISE EXCEPTION 'invocation tenant identity is immutable'
        USING ERRCODE = '23514', CONSTRAINT = 'invocation_platform_tenant_identity';
    END IF;
  END IF;
  IF NEW.platform_tenant_id IS NULL THEN RETURN NEW; END IF;
  IF TG_OP = 'INSERT' OR (OLD.state = 'pending' AND NEW.state = 'dispatching') THEN
    SELECT t.status INTO tenant_status FROM platform_tenants t JOIN apps a ON a.account_id = t.account_id
      WHERE t.id = NEW.platform_tenant_id AND t.account_id = NEW.account_id
        AND a.id = NEW.app_id
      FOR SHARE OF t, a;
    IF NOT FOUND THEN
      IF TG_OP = 'UPDATE' THEN RETURN NULL; END IF;
      RAISE EXCEPTION 'invocation tenant must be active and own the app account'
        USING ERRCODE = '23514', CONSTRAINT = 'invocation_platform_tenant_identity';
    END IF;
    IF tenant_status <> 'active' THEN
      IF TG_OP = 'UPDATE' THEN RETURN NULL; END IF;
      RAISE EXCEPTION 'invocation platform tenant is suspended'
        USING ERRCODE = '23514', CONSTRAINT = 'invocation_platform_tenant_active';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS invocation_platform_tenant_guard ON invocations;
CREATE TRIGGER invocation_platform_tenant_guard BEFORE INSERT OR UPDATE ON invocations
  FOR EACH ROW EXECUTE FUNCTION guard_invocation_platform_tenant();

ALTER TABLE platform_tenant_access_tokens
  DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens
  ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
    cardinality(scopes) > 0 AND scopes <@ ARRAY[
      'platform_tenant:usage:read', 'platform_tenant:statements:read',
      'platform_tenant:activation:read', 'platform_tenant:hostnames:manage',
      'platform_tenant:credentials:read', 'platform_tenant:credentials:manage',
      'platform_tenant:consumers:manage', 'platform_tenant:invocations:read',
      'platform_tenant:invocations:manage'
    ]::text[]
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM platform_tenant_access_tokens WHERE scopes <@ ARRAY[
  'platform_tenant:invocations:read', 'platform_tenant:invocations:manage']::text[];
UPDATE platform_tenant_access_tokens SET scopes = array_remove(
  array_remove(scopes, 'platform_tenant:invocations:read'), 'platform_tenant:invocations:manage');
ALTER TABLE platform_tenant_access_tokens DROP CONSTRAINT IF EXISTS platform_tenant_access_tokens_scopes_check;
ALTER TABLE platform_tenant_access_tokens ADD CONSTRAINT platform_tenant_access_tokens_scopes_check CHECK (
  cardinality(scopes) > 0 AND scopes <@ ARRAY[
    'platform_tenant:usage:read', 'platform_tenant:statements:read',
    'platform_tenant:activation:read', 'platform_tenant:hostnames:manage',
    'platform_tenant:credentials:read', 'platform_tenant:credentials:manage',
    'platform_tenant:consumers:manage']::text[]);
DROP TRIGGER IF EXISTS invocation_platform_tenant_guard ON invocations;
DROP FUNCTION IF EXISTS guard_invocation_platform_tenant();
ALTER TABLE invocations DROP COLUMN IF EXISTS platform_tenant_id;
-- +goose StatementEnd
