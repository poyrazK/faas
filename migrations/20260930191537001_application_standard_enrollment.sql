-- +goose Up
CREATE TABLE application_standard_assignments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('organization', 'project', 'application')),
    scope_id uuid NOT NULL,
    standard_id uuid NOT NULL,
    admission_version bigint NOT NULL CHECK (admission_version BETWEEN 1 AND 9007199254740991),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    active boolean NOT NULL DEFAULT false,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (scope <> 'organization' OR scope_id = org_id),
    UNIQUE (org_id, scope, scope_id, standard_id),
    UNIQUE (org_id, id),
    FOREIGN KEY (org_id, standard_id, admission_version)
        REFERENCES application_standard_versions(org_id, standard_id, version) ON DELETE CASCADE
);
CREATE INDEX application_standard_assignments_scope_idx
ON application_standard_assignments (scope, scope_id, org_id) WHERE active;

CREATE TABLE app_application_standards (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    project_id uuid,
    base_settings jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(base_settings) = 'object'),
    local_settings jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(local_settings) = 'object'),
    additional_log_destinations uuid[] NOT NULL DEFAULT '{}',
    adoptions jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(adoptions) = 'array'),
    effective jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(effective) = 'object'),
    effective_hash text NOT NULL DEFAULT '' CHECK (effective_hash = '' OR effective_hash ~ '^[a-f0-9]{64}$'),
    desired_revision bigint NOT NULL DEFAULT 1 CHECK (desired_revision > 0),
    persisted_revision bigint NOT NULL DEFAULT 0 CHECK (persisted_revision >= 0 AND persisted_revision <= desired_revision),
    observed_revision bigint NOT NULL DEFAULT 0 CHECK (observed_revision >= 0 AND observed_revision <= persisted_revision),
    state text NOT NULL DEFAULT 'unmanaged' CHECK (state IN ('unmanaged', 'pending', 'applying', 'persisted', 'observed', 'blocked')),
    error_code text NOT NULL DEFAULT '' CHECK (error_code ~ '^[a-z0-9_]{0,128}$'),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX app_application_standards_pending_idx ON app_application_standards (updated_at, app_id)
WHERE state IN ('pending', 'blocked');

-- Organization locks fence membership against reviewed assignment activation.
-- The insert lock is shared: concurrent service creation is not serialized.
-- +goose StatementBegin
CREATE FUNCTION application_standard_app_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER application_standard_app_scope_guard
BEFORE INSERT OR UPDATE OF org_id, project_id ON apps
FOR EACH ROW EXECUTE FUNCTION application_standard_app_scope_guard();

-- +goose StatementBegin
CREATE FUNCTION application_standard_assignment_scope_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER application_standard_assignment_scope_guard
BEFORE INSERT OR UPDATE ON application_standard_assignments
FOR EACH ROW EXECUTE FUNCTION application_standard_assignment_scope_guard();

-- All app INSERT paths, including raw project/preview inserts, capture the
-- original values and admission pins before any deployment may be created.
-- Scope changes retain local intent instead of copying the old projection.
-- +goose StatementBegin
CREATE FUNCTION application_standard_enroll_app() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pins jsonb;
BEGIN
    SELECT coalesce(jsonb_agg(jsonb_build_object('assignment_id', id::text, 'version', admission_version)
                             ORDER BY id), '[]'::jsonb)
      INTO pins FROM application_standard_assignments
      WHERE active AND org_id = NEW.org_id
        AND ((scope = 'organization' AND scope_id = NEW.org_id)
          OR (scope = 'project' AND scope_id = NEW.project_id)
          OR (scope = 'application' AND scope_id = NEW.id));
    INSERT INTO app_application_standards (app_id, org_id, project_id, base_settings, adoptions, state)
    VALUES (NEW.id, NEW.org_id, NEW.project_id,
            jsonb_build_object('require_signed', NEW.require_signed, 'security_policy', NEW.security_policy,
                               'egress_cidrs', to_jsonb(NEW.egress_allowlist::text[]),
                               'egress_extra_ports', to_jsonb(NEW.egress_ports)),
            pins, CASE WHEN pins = '[]'::jsonb THEN 'unmanaged' ELSE 'pending' END)
    ON CONFLICT (app_id) DO UPDATE SET
        org_id = EXCLUDED.org_id, project_id = EXCLUDED.project_id, adoptions = EXCLUDED.adoptions,
        state = EXCLUDED.state, desired_revision = app_application_standards.desired_revision + 1,
        effective = '{}'::jsonb, effective_hash = '', persisted_revision = 0, observed_revision = 0,
        error_code = '', updated_at = now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_enroll_app
AFTER INSERT ON apps FOR EACH ROW EXECUTE FUNCTION application_standard_enroll_app();
CREATE TRIGGER application_standard_reenroll_app
AFTER UPDATE OF org_id, project_id, status ON apps FOR EACH ROW
WHEN (OLD.org_id IS DISTINCT FROM NEW.org_id OR OLD.project_id IS DISTINCT FROM NEW.project_id
      OR (OLD.status = 'deleted' AND NEW.status <> 'deleted'))
EXECUTE FUNCTION application_standard_enroll_app();

INSERT INTO app_application_standards (app_id, org_id, project_id, base_settings)
SELECT id, org_id, project_id,
       jsonb_build_object('require_signed', require_signed, 'security_policy', security_policy,
                          'egress_cidrs', to_jsonb(egress_allowlist::text[]), 'egress_extra_ports', to_jsonb(egress_ports))
FROM apps;

-- Pending or failed enrollment cannot be bypassed by a different deployment
-- entry point. The app row lock serializes this check with materialization.
-- +goose StatementBegin
CREATE FUNCTION application_standard_deployment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM apps WHERE id = NEW.app_id FOR UPDATE;
    IF EXISTS (SELECT 1 FROM app_application_standards WHERE app_id = NEW.app_id
               AND state IN ('pending', 'applying', 'blocked')) THEN
        RAISE EXCEPTION 'application standards enrollment is not persisted'
            USING ERRCODE = '23514', CONSTRAINT = 'application_standards_pending';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_deployment_guard BEFORE INSERT ON deployments
FOR EACH ROW EXECUTE FUNCTION application_standard_deployment_guard();

-- +goose Down
DROP TRIGGER application_standard_deployment_guard ON deployments;
DROP FUNCTION application_standard_deployment_guard();
DROP TRIGGER application_standard_reenroll_app ON apps;
DROP TRIGGER application_standard_enroll_app ON apps;
DROP FUNCTION application_standard_enroll_app();
DROP TRIGGER application_standard_assignment_scope_guard ON application_standard_assignments;
DROP FUNCTION application_standard_assignment_scope_guard();
DROP TRIGGER application_standard_app_scope_guard ON apps;
DROP FUNCTION application_standard_app_scope_guard();
DROP TABLE app_application_standards;
DROP TABLE application_standard_assignments;
