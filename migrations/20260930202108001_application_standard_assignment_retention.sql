-- +goose Up
-- Assignment removal changes mandatory inheritance. Reviewed operations will
-- deactivate an assignment and advance its revision; deleting its identity is
-- reserved for erasing its owning organization.
-- +goose StatementBegin
CREATE FUNCTION application_standard_assignment_retention_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF pg_trigger_depth() < 2 OR EXISTS (SELECT 1 FROM orgs WHERE id = OLD.org_id) THEN
        RAISE EXCEPTION 'application standard assignments must be deactivated through review'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_assignment_retention';
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_assignment_retention_guard
BEFORE DELETE ON application_standard_assignments
FOR EACH ROW EXECUTE FUNCTION application_standard_assignment_retention_guard();

-- +goose Down
DROP TRIGGER application_standard_assignment_retention_guard ON application_standard_assignments;
DROP FUNCTION application_standard_assignment_retention_guard();
