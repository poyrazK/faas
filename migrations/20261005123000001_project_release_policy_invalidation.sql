-- adr: 590
-- +goose Up
-- +goose StatementBegin
-- Ordinary ingress reads settings from the active production graph. Refresh
-- cached settings after publication, deactivation, or deletion, in the same
-- transaction as the graph change. The ledger repairs a lost NOTIFY.
CREATE FUNCTION record_project_release_policy_change(release_uuid uuid, app_uuid uuid)
RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(711901248671::bigint);
    INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
    VALUES ('project_release', release_uuid, app_uuid, 'updated');
    PERFORM pg_notify('app_changed', app_uuid::text);
END;
$$;

CREATE FUNCTION project_release_member_policy_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- The parent is inserted before its immutable members. Inactive retained
    -- graphs and stage graph publication cannot change ordinary ingress.
    IF EXISTS (SELECT 1 FROM project_release_sets
               WHERE id = NEW.release_id AND active AND environment_slug = 'production') THEN
        PERFORM record_project_release_policy_change(NEW.release_id, NEW.app_id);
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER project_release_member_policy_changed_trg
AFTER INSERT ON project_release_members
FOR EACH ROW EXECUTE FUNCTION project_release_member_policy_changed();

CREATE FUNCTION project_release_set_policy_changed()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE member_app uuid;
BEGIN
    IF OLD.environment_slug = 'production' THEN
        FOR member_app IN SELECT app_id FROM project_release_members WHERE release_id = OLD.id LOOP
            PERFORM record_project_release_policy_change(OLD.id, member_app);
        END LOOP;
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER project_release_set_policy_changed_trg
AFTER UPDATE OF active ON project_release_sets
FOR EACH ROW WHEN (OLD.active IS DISTINCT FROM NEW.active)
EXECUTE FUNCTION project_release_set_policy_changed();

-- Run before FK cascades remove the members needed to identify affected apps.
CREATE TRIGGER project_release_set_policy_deleted_trg
BEFORE DELETE ON project_release_sets
FOR EACH ROW WHEN (OLD.active)
EXECUTE FUNCTION project_release_set_policy_changed();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS project_release_set_policy_deleted_trg ON project_release_sets;
DROP TRIGGER IF EXISTS project_release_set_policy_changed_trg ON project_release_sets;
DROP TRIGGER IF EXISTS project_release_member_policy_changed_trg ON project_release_members;
DROP FUNCTION IF EXISTS project_release_set_policy_changed();
DROP FUNCTION IF EXISTS project_release_member_policy_changed();
DROP FUNCTION IF EXISTS record_project_release_policy_change(uuid, uuid);
-- +goose StatementEnd
