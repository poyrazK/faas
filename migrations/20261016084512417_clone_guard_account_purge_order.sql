-- filename: 20261016084512417_clone_guard_account_purge_order.sql

-- +goose Up
-- H10-94: an account purge cascades to projects and, through the guard's own
-- account_id foreign key, to the clone configuration guard. PostgreSQL fires
-- the account's cascade triggers in trigger-name order, and those names embed
-- OIDs, so the guard can be removed before the project. The project's BEFORE
-- DELETE fence then saw a live project without its guard and refused the
-- admitted purge. A project whose account is already gone is part of that
-- purge; a live project in a live account with missing evidence still fails
-- closed.
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
        -- The project or account BEFORE DELETE guard already admitted this
        -- deletion. FK cascades can remove the guard before dependent rows,
        -- or before the project itself, are cleaned. A live project in a
        -- live account with missing evidence must still fail closed.
        IF NOT EXISTS (
            SELECT 1 FROM projects p JOIN accounts a ON a.id = p.account_id
            WHERE p.id = project
        ) THEN RETURN; END IF;
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
