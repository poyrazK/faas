-- +goose Up
-- Legacy projects may contain apps owned by different organizations. A project
-- row lock fences assignment validation against every new/reenrolled member,
-- including inserts by a different organization. An org lock alone cannot
-- serialize that race. Ordinary app creation uses a shared project lock.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_app_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM orgs WHERE id = NEW.org_id FOR SHARE;
    IF NEW.project_id IS NOT NULL THEN
        PERFORM 1 FROM projects WHERE id = NEW.project_id FOR SHARE;
    END IF;
    IF EXISTS (
        SELECT 1 FROM application_standard_assignments a
        WHERE a.active AND a.org_id <> NEW.org_id
          AND ((a.scope = 'project' AND a.scope_id = NEW.project_id)
            OR (a.scope = 'application' AND a.scope_id = NEW.id))
    ) THEN
        RAISE EXCEPTION 'application scope is assigned to another organization'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_assignment_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE creator uuid;
BEGIN
    PERFORM 1 FROM orgs WHERE id = NEW.org_id FOR UPDATE;
    IF NEW.scope = 'application' THEN
        IF NOT EXISTS (SELECT 1 FROM apps WHERE id = NEW.scope_id AND org_id = NEW.org_id AND status <> 'deleted') THEN
            RAISE EXCEPTION 'application standard scope does not belong to its organization'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    ELSIF NEW.scope = 'project' THEN
        PERFORM 1 FROM projects WHERE id = NEW.scope_id FOR UPDATE;
        IF EXISTS (SELECT 1 FROM application_standard_assignments
                   WHERE active AND scope = 'project' AND scope_id = NEW.scope_id AND org_id <> NEW.org_id) THEN
            RAISE EXCEPTION 'project scope is already assigned to another organization'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
        SELECT account_id INTO creator FROM projects WHERE id = NEW.scope_id;
        IF creator IS NULL OR NOT EXISTS (
            SELECT 1 FROM orgs o WHERE o.id = NEW.org_id AND
              (o.personal_owner_account_id = creator OR EXISTS (
                SELECT 1 FROM org_memberships m WHERE m.org_id = o.id AND m.account_id = creator AND m.removed_at IS NULL
              ))
        ) OR EXISTS (SELECT 1 FROM apps WHERE project_id = NEW.scope_id AND status <> 'deleted' AND org_id <> NEW.org_id) THEN
            RAISE EXCEPTION 'project standard scope has no verified organization owner'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_app_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM orgs WHERE id = NEW.org_id FOR SHARE;
    IF EXISTS (
        SELECT 1 FROM application_standard_assignments a
        WHERE a.active AND a.org_id <> NEW.org_id
          AND ((a.scope = 'project' AND a.scope_id = NEW.project_id)
            OR (a.scope = 'application' AND a.scope_id = NEW.id))
    ) THEN
        RAISE EXCEPTION 'application scope is assigned to another organization'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_assignment_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE creator uuid;
BEGIN
    PERFORM 1 FROM orgs WHERE id = NEW.org_id FOR UPDATE;
    IF NEW.scope = 'application' THEN
        IF NOT EXISTS (SELECT 1 FROM apps WHERE id = NEW.scope_id AND org_id = NEW.org_id AND status <> 'deleted') THEN
            RAISE EXCEPTION 'application standard scope does not belong to its organization'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    ELSIF NEW.scope = 'project' THEN
        SELECT account_id INTO creator FROM projects WHERE id = NEW.scope_id;
        IF creator IS NULL OR NOT EXISTS (
            SELECT 1 FROM orgs o WHERE o.id = NEW.org_id AND
              (o.personal_owner_account_id = creator OR EXISTS (
                SELECT 1 FROM org_memberships m WHERE m.org_id = o.id AND m.account_id = creator AND m.removed_at IS NULL
              ))
        ) OR EXISTS (SELECT 1 FROM apps WHERE project_id = NEW.scope_id AND status <> 'deleted' AND org_id <> NEW.org_id) THEN
            RAISE EXCEPTION 'project standard scope has no verified organization owner'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
