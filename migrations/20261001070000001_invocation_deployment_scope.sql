-- +goose Up
-- +goose StatementBegin
-- An accepted invocation keeps its environment when an app is adopted into a
-- project or its project is removed. Retries and replays reuse this identity.
ALTER TABLE invocations ADD COLUMN deployment_scope text;
UPDATE invocations i SET deployment_scope = CASE
  WHEN a.project_id IS NOT NULL AND coalesce(a.preview_of_slug, '') = ''
    THEN 'production' ELSE 'default' END
FROM apps a WHERE a.id = i.app_id;

CREATE FUNCTION guard_invocation_deployment_scope() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' AND (NEW.deployment_scope IS NULL OR NEW.deployment_scope = '') THEN
    SELECT CASE WHEN a.project_id IS NOT NULL AND coalesce(a.preview_of_slug, '') = ''
      THEN 'production' ELSE 'default' END INTO NEW.deployment_scope
      FROM apps a WHERE a.id = NEW.app_id FOR SHARE OF a;
  ELSIF TG_OP = 'UPDATE' AND NEW.deployment_scope IS DISTINCT FROM OLD.deployment_scope THEN
    RAISE EXCEPTION 'invocation deployment scope is immutable'
      USING ERRCODE = '23514', CONSTRAINT = 'invocation_deployment_scope_identity';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER invocation_deployment_scope_guard BEFORE INSERT OR UPDATE ON invocations
  FOR EACH ROW EXECUTE FUNCTION guard_invocation_deployment_scope();
ALTER TABLE invocations ALTER COLUMN deployment_scope SET NOT NULL;
ALTER TABLE invocations ADD CONSTRAINT invocation_deployment_scope_check
  CHECK (deployment_scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER invocation_deployment_scope_guard ON invocations;
DROP FUNCTION guard_invocation_deployment_scope();
ALTER TABLE invocations DROP COLUMN deployment_scope;
-- +goose StatementEnd
