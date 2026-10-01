-- +goose Up
ALTER TABLE app_application_standards ADD COLUMN materialized_fields text[] NOT NULL DEFAULT '{}'
 CHECK (materialized_fields <@ ARRAY['log_destinations','require_signed','security_policy','trusted_publishers','egress_cidrs','egress_extra_ports']::text[]);
UPDATE app_application_standards e SET materialized_fields = coalesce((SELECT array_agg(s.key ORDER BY s.key)
 FROM jsonb_each(e.effective->'sources') s WHERE jsonb_typeof(s.value)='array' AND jsonb_array_length(s.value)>0),'{}'::text[]);

-- Keep the last installed field context while the desired ownership is reset.
-- A move out of all assignments still needs repair to restore those fields.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_enroll_app() RETURNS trigger LANGUAGE plpgsql AS $$
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_enrollment_generation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.lease_generation < OLD.lease_generation THEN
        RAISE EXCEPTION 'application standard enrollment generation regressed'
          USING ERRCODE='23514',CONSTRAINT='application_standard_enrollment_generation';
    END IF;
    IF NEW.app_id IS DISTINCT FROM OLD.app_id THEN
        RAISE EXCEPTION 'application standard enrollment identity changed'
          USING ERRCODE='23514',CONSTRAINT='application_standard_enrollment_identity';
    END IF;
    IF NEW.org_id IS DISTINCT FROM OLD.org_id OR NEW.project_id IS DISTINCT FROM OLD.project_id
      OR NEW.base_settings IS DISTINCT FROM OLD.base_settings OR NEW.local_settings IS DISTINCT FROM OLD.local_settings
      OR NEW.additional_log_destinations IS DISTINCT FROM OLD.additional_log_destinations OR NEW.adoptions IS DISTINCT FROM OLD.adoptions
      OR NEW.desired_revision IS DISTINCT FROM OLD.desired_revision OR NEW.effective IS DISTINCT FROM OLD.effective
      OR NEW.effective_hash IS DISTINCT FROM OLD.effective_hash OR NEW.materialized_fields IS DISTINCT FROM OLD.materialized_fields THEN
        NEW.lease_owner := ''; NEW.lease_until := NULL;
        NEW.lease_generation := greatest(NEW.lease_generation,OLD.lease_generation+1);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- During reenrollment, legacy edits cannot replace previously managed intent.
-- The materializer switches to applying inside its installation transaction;
-- ordinary projection guards then permit only the resolved configuration.
-- +goose StatementBegin
CREATE FUNCTION application_standard_pending_scalar_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE fields text[];
BEGIN
    SELECT materialized_fields INTO fields FROM app_application_standards WHERE app_id=OLD.id AND state IN ('pending','blocked');
    IF ('require_signed'=ANY(fields) AND NEW.require_signed IS DISTINCT FROM OLD.require_signed)
      OR ('security_policy'=ANY(fields) AND NEW.security_policy IS DISTINCT FROM OLD.security_policy)
      OR ('egress_cidrs'=ANY(fields) AND NEW.egress_allowlist IS DISTINCT FROM OLD.egress_allowlist)
      OR ('egress_extra_ports'=ANY(fields) AND NEW.egress_ports IS DISTINCT FROM OLD.egress_ports) THEN
        RAISE EXCEPTION 'application standard manages this control'
          USING ERRCODE='23514',CONSTRAINT='application_standard_managed_control';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_pending_scalar_guard BEFORE UPDATE OF require_signed,security_policy,egress_allowlist,egress_ports ON apps
FOR EACH ROW EXECUTE FUNCTION application_standard_pending_scalar_guard();

-- +goose StatementBegin
CREATE FUNCTION application_standard_pending_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE app_ids uuid[] := '{}';
BEGIN
    IF TG_OP <> 'INSERT' THEN app_ids:=array_append(app_ids,OLD.app_id); END IF;
    IF TG_OP <> 'DELETE' THEN app_ids:=array_append(app_ids,NEW.app_id); END IF;
    IF EXISTS (SELECT 1 FROM app_application_standards e JOIN apps a ON a.id=e.app_id
      WHERE e.app_id=ANY(app_ids) AND a.status <> 'deleted' AND e.state IN ('pending','blocked') AND TG_ARGV[0]=ANY(e.materialized_fields)) THEN
      IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at')=(to_jsonb(OLD)-'updated_at') THEN RETURN NEW; END IF;
      RAISE EXCEPTION 'application standard manages this control'
        USING ERRCODE='23514',CONSTRAINT='application_standard_managed_control';
    END IF;
    IF TG_OP='DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER application_standard_w_pending_drain_guard BEFORE INSERT OR UPDATE OR DELETE ON app_log_drains
FOR EACH ROW EXECUTE FUNCTION application_standard_pending_child_guard('log_destinations');
CREATE TRIGGER application_standard_w_pending_signer_guard BEFORE INSERT OR UPDATE OR DELETE ON app_trusted_signers
FOR EACH ROW EXECUTE FUNCTION application_standard_pending_child_guard('trusted_publishers');

-- +goose Down
DROP TRIGGER application_standard_w_pending_signer_guard ON app_trusted_signers;
DROP TRIGGER application_standard_w_pending_drain_guard ON app_log_drains;
DROP FUNCTION application_standard_pending_child_guard();
DROP TRIGGER application_standard_pending_scalar_guard ON apps;
DROP FUNCTION application_standard_pending_scalar_guard();
-- The retained field column is also used by the replaced generation/enrollment
-- functions. Keep this restoration context on downgrade rather than orphaning
-- those functions or losing an application's original baseline.
