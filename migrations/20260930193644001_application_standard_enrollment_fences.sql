-- +goose Up
-- Restoring a tombstone changes membership just as creating a service does.
-- Ordinary scheduler status changes do not need an organization membership lock.
DROP TRIGGER application_standard_app_scope_guard ON apps;
CREATE TRIGGER application_standard_app_scope_insert_guard
BEFORE INSERT ON apps FOR EACH ROW EXECUTE FUNCTION application_standard_app_scope_guard();
CREATE TRIGGER application_standard_app_scope_update_guard
BEFORE UPDATE OF org_id, project_id, status ON apps FOR EACH ROW
WHEN (OLD.org_id IS DISTINCT FROM NEW.org_id OR OLD.project_id IS DISTINCT FROM NEW.project_id
      OR (OLD.status = 'deleted' AND NEW.status <> 'deleted'))
EXECUTE FUNCTION application_standard_app_scope_guard();

-- A reviewed assignment retains its identity. Changing admission or activation
-- is a new revision; a no-op write cannot silently edit that review boundary.
-- +goose StatementBegin
CREATE FUNCTION application_standard_assignment_revision_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.org_id IS DISTINCT FROM OLD.org_id
       OR NEW.scope IS DISTINCT FROM OLD.scope OR NEW.scope_id IS DISTINCT FROM OLD.scope_id
       OR NEW.standard_id IS DISTINCT FROM OLD.standard_id
       OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'application standard assignment identity or revision changed'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_assignment_revision';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_assignment_revision_guard
BEFORE UPDATE ON application_standard_assignments
FOR EACH ROW EXECUTE FUNCTION application_standard_assignment_revision_guard();

-- +goose Down
DROP TRIGGER application_standard_assignment_revision_guard ON application_standard_assignments;
DROP FUNCTION application_standard_assignment_revision_guard();
DROP TRIGGER application_standard_app_scope_update_guard ON apps;
DROP TRIGGER application_standard_app_scope_insert_guard ON apps;
CREATE TRIGGER application_standard_app_scope_guard
BEFORE INSERT OR UPDATE OF org_id, project_id ON apps
FOR EACH ROW EXECUTE FUNCTION application_standard_app_scope_guard();
