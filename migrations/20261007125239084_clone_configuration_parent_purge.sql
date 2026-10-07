-- +goose Up
-- ADR-590: preserve admitted project deletion across FK cascade ordering.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assert_clone_configuration_mutable(project uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    guard_state text;
BEGIN
    IF project IS NULL THEN RETURN; END IF;
    -- A locking read also prevents repeatable-read writers with a snapshot
    -- predating acquisition from bypassing the committed hold.
    SELECT state INTO guard_state FROM project_environment_clone_configuration_guards
    WHERE project_id = project FOR SHARE;
    IF NOT FOUND THEN
        -- The project BEFORE DELETE guard already admitted this deletion.
        -- FK cascades can remove the guard before dependent rows are cleaned.
        -- A live project with missing evidence must still fail closed.
        IF NOT EXISTS (SELECT 1 FROM projects WHERE id = project) THEN RETURN; END IF;
        RAISE EXCEPTION 'project configuration guard is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    IF guard_state <> 'open' THEN
        RAISE EXCEPTION 'source configuration is held for stage capture'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_write_fenced';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assert_clone_configuration_mutable(project uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    guard_state text;
BEGIN
    IF project IS NULL THEN RETURN; END IF;
    -- A locking read also prevents repeatable-read writers with a snapshot
    -- predating acquisition from bypassing the committed hold.
    SELECT state INTO guard_state FROM project_environment_clone_configuration_guards
    WHERE project_id = project FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'project configuration guard is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    IF guard_state <> 'open' THEN
        RAISE EXCEPTION 'source configuration is held for stage capture'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_write_fenced';
    END IF;
END;
$$;
-- +goose StatementEnd
