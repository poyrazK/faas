-- +goose Up
-- Runtime eviction preserves workload membership; only deletion removes it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_gitops_guard_app_presence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE src environment_git_sources%ROWTYPE;
BEGIN
    IF pg_trigger_depth() > 1 THEN
        IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.status = 'deleted') = (OLD.status = 'deleted') THEN RETURN NEW; END IF;
    FOR src IN SELECT s.* FROM environment_git_sources s
        JOIN environment_gitops_resources r ON r.source_id = s.id
        WHERE r.app_id = OLD.id ORDER BY s.id FOR UPDATE OF s
    LOOP
        IF (TG_OP = 'DELETE' OR NEW.status = 'deleted') AND src.mode = 'enforce' AND EXISTS (
            SELECT 1 FROM environment_managed_fields f JOIN environment_gitops_resources r
            ON r.source_id = f.source_id AND r.logical_name = f.resource
            WHERE f.source_id = src.id AND r.app_id = OLD.id AND f.field_path = 'presence'
              AND NOT EXISTS (SELECT 1 FROM environment_management_overrides o
                  WHERE o.environment_id = f.environment_id AND o.resource = f.resource AND o.field_path = 'presence'
                    AND o.expires_at > clock_timestamp())) THEN
            RAISE EXCEPTION 'workload membership is managed by the environment Git source'
                USING ERRCODE = '23514', CONSTRAINT = 'environment_gitops_field_owned';
        END IF;
        UPDATE environment_git_sources SET intent_version = intent_version + 1, updated_at = now() WHERE id = src.id;
        IF src.generation > 0 THEN
            INSERT INTO environment_gitops_jobs(source_id, desired_generation, next_attempt_at)
            VALUES (src.id, src.generation, now()) ON CONFLICT (source_id) DO UPDATE
            SET next_attempt_at = least(environment_gitops_jobs.next_attempt_at, excluded.next_attempt_at);
        END IF;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Preserve durable ownership, accepted work and runtime evidence on rollback.
SELECT 1;
