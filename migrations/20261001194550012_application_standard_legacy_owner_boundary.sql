-- filename: 20261001194550012_application_standard_legacy_owner_boundary.sql
-- adr: 393. Unowned legacy inserts retain compatibility; nullable owners
-- cannot evade company assignments or verified project membership.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_app_scope_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF OLD.org_id IS NOT NULL AND NEW.org_id IS NULL THEN
            RAISE EXCEPTION 'application organization owner cannot be removed'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    END IF;
    PERFORM 1 FROM orgs WHERE id = NEW.org_id FOR SHARE;
    IF NEW.project_id IS NOT NULL THEN
        PERFORM 1 FROM projects WHERE id = NEW.project_id FOR SHARE;
    END IF;
    IF EXISTS (
        SELECT 1 FROM application_standard_assignments a
        WHERE a.active AND a.org_id IS DISTINCT FROM NEW.org_id
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
CREATE OR REPLACE FUNCTION application_standard_assignment_scope_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
                   WHERE active AND scope = 'project' AND scope_id = NEW.scope_id AND org_id IS DISTINCT FROM NEW.org_id) THEN
            RAISE EXCEPTION 'project scope is already assigned to another organization'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
        SELECT account_id INTO creator FROM projects WHERE id = NEW.scope_id;
        IF creator IS NULL OR NOT EXISTS (
            SELECT 1 FROM orgs o WHERE o.id = NEW.org_id AND
              (o.personal_owner_account_id = creator OR EXISTS (
                SELECT 1 FROM org_memberships m WHERE m.org_id = o.id AND m.account_id = creator AND m.removed_at IS NULL
              ))
        ) OR EXISTS (SELECT 1 FROM apps WHERE project_id = NEW.scope_id AND status <> 'deleted' AND org_id IS DISTINCT FROM NEW.org_id) THEN
            RAISE EXCEPTION 'project standard scope has no verified organization owner'
                USING ERRCODE = '23514', CONSTRAINT = 'application_standard_scope_owner';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_enroll_app() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE pins jsonb;
BEGIN
    IF NEW.org_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT coalesce(jsonb_agg(jsonb_build_object('assignment_id', id::text, 'version', admission_version) ORDER BY id), '[]'::jsonb)
    INTO pins FROM application_standard_assignments WHERE active AND org_id = NEW.org_id
      AND ((scope = 'organization' AND scope_id = NEW.org_id)
        OR (scope = 'project' AND scope_id = NEW.project_id) OR (scope = 'application' AND scope_id = NEW.id));
    INSERT INTO app_application_standards (app_id, org_id, project_id, base_settings, adoptions, state)
    VALUES (NEW.id, NEW.org_id, NEW.project_id,
      jsonb_build_object('require_signed', NEW.require_signed, 'security_policy', NEW.security_policy,
        'egress_cidrs', to_jsonb(NEW.egress_allowlist::text[]), 'egress_extra_ports', to_jsonb(NEW.egress_ports)),
      pins, CASE WHEN pins = '[]'::jsonb THEN 'unmanaged' ELSE 'pending' END)
    ON CONFLICT (app_id) DO UPDATE SET org_id = EXCLUDED.org_id, project_id = EXCLUDED.project_id, adoptions = EXCLUDED.adoptions,
      state = CASE WHEN EXCLUDED.adoptions <> '[]'::jsonb OR cardinality(app_application_standards.materialized_fields)>0 THEN 'pending' ELSE 'unmanaged' END,
      desired_revision = app_application_standards.desired_revision + 1, effective = '{}'::jsonb, effective_hash = '',
      persisted_revision = 0, observed_revision = 0, error_code = '', updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_app_scope_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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
CREATE OR REPLACE FUNCTION application_standard_assignment_scope_guard() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_enroll_app() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE pins jsonb;
BEGIN
    SELECT coalesce(jsonb_agg(jsonb_build_object('assignment_id', id::text, 'version', admission_version) ORDER BY id), '[]'::jsonb)
    INTO pins FROM application_standard_assignments WHERE active AND org_id = NEW.org_id
      AND ((scope = 'organization' AND scope_id = NEW.org_id)
        OR (scope = 'project' AND scope_id = NEW.project_id) OR (scope = 'application' AND scope_id = NEW.id));
    INSERT INTO app_application_standards (app_id, org_id, project_id, base_settings, adoptions, state)
    VALUES (NEW.id, NEW.org_id, NEW.project_id,
      jsonb_build_object('require_signed', NEW.require_signed, 'security_policy', NEW.security_policy,
        'egress_cidrs', to_jsonb(NEW.egress_allowlist::text[]), 'egress_extra_ports', to_jsonb(NEW.egress_ports)),
      pins, CASE WHEN pins = '[]'::jsonb THEN 'unmanaged' ELSE 'pending' END)
    ON CONFLICT (app_id) DO UPDATE SET org_id = EXCLUDED.org_id, project_id = EXCLUDED.project_id, adoptions = EXCLUDED.adoptions,
      state = CASE WHEN EXCLUDED.adoptions <> '[]'::jsonb OR cardinality(app_application_standards.materialized_fields)>0 THEN 'pending' ELSE 'unmanaged' END,
      desired_revision = app_application_standards.desired_revision + 1, effective = '{}'::jsonb, effective_hash = '',
      persisted_revision = 0, observed_revision = 0, error_code = '', updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

