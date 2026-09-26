-- +goose Up
-- +goose StatementBegin
-- A ledger ID is useful as a replay cursor only if transactions acquire IDs
-- in commit order. All writers of this broadcast log take the same
-- transaction-scoped advisory lock before INSERT; a later writer cannot
-- allocate an ID until the previous writer has committed or rolled back.
CREATE OR REPLACE FUNCTION apps_record_control_plane_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Scale-in/out bookkeeping changes often and is not gateway policy.
    -- Avoid serializing those writes behind the global replay-order lock or
    -- claiming that a gateway has a new policy revision to apply.
    IF TG_OP = 'UPDATE' THEN
        IF (to_jsonb(NEW) - ARRAY['last_scale_out_at', 'last_scale_in_at']) =
           (to_jsonb(OLD) - ARRAY['last_scale_out_at', 'last_scale_in_at']) THEN
            RETURN NEW;
        END IF;
    END IF;
    PERFORM pg_advisory_xact_lock(711901248671::bigint);
    IF TG_OP = 'INSERT' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'created');
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'updated');
        RETURN NEW;
    ELSE
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', OLD.id, OLD.id, 'deleted');
        RETURN OLD;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION deployments_record_traffic_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(711901248671::bigint);
    INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
    VALUES ('deployment_traffic', NEW.id, NEW.app_id, 'updated');
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS deployments_record_traffic_change_trg ON deployments;
CREATE TRIGGER deployments_record_traffic_change_trg
    AFTER UPDATE OF traffic_percent ON deployments
    FOR EACH ROW
    WHEN (OLD.traffic_percent IS DISTINCT FROM NEW.traffic_percent)
    EXECUTE FUNCTION deployments_record_traffic_change();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_record_traffic_change_trg ON deployments;
DROP FUNCTION IF EXISTS deployments_record_traffic_change();

CREATE OR REPLACE FUNCTION apps_record_control_plane_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'created');
        RETURN NEW;
    ELSIF TG_OP = 'UPDATE' THEN
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', NEW.id, NEW.id, 'updated');
        RETURN NEW;
    ELSE
        INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation)
        VALUES ('app', OLD.id, OLD.id, 'deleted');
        RETURN OLD;
    END IF;
END;
$$;
-- +goose StatementEnd
