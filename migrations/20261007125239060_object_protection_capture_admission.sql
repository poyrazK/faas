-- +goose Up
-- ADR-590: retention and legal-hold mutations use their original durable journal
-- as writer evidence. Count waiting/applying intents until recovery settles
-- them; a capture hold closes only new admission, never original settlement.
-- Enforce admission for replicas predating the Go-side check too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION fence_object_protection_capture_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bucket_state text;
BEGIN
 -- Fence queries need a fresh snapshot after waiting for the source lock.
 -- Reject old transaction snapshots rather than hiding a committed hold.
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_protection_admission_isolation',MESSAGE='Protection admission requires READ COMMITTED';
 END IF;
 SELECT state INTO bucket_state FROM object_buckets WHERE id=NEW.bucket_id FOR UPDATE;
 IF bucket_state IS DISTINCT FROM 'ready' THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_protection_bucket_not_ready',MESSAGE='Bucket cleanup fences protection admission';
 END IF;
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences WHERE bucket_id=NEW.bucket_id) THEN
  RAISE EXCEPTION USING ERRCODE='55000',CONSTRAINT='object_protection_capture_fenced',MESSAGE='Checkpoint capture fences new protection admission';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS object_protection_capture_admission ON object_version_protection;
CREATE TRIGGER object_protection_capture_admission BEFORE INSERT ON object_version_protection
 FOR EACH ROW EXECUTE FUNCTION fence_object_protection_capture_admission();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_clone_configuration_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    before_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
    after_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) ELSE '{}'::jsonb END;
    before_id uuid := nullif(before_row ->> TG_ARGV[1], '')::uuid;
    after_id uuid := nullif(after_row ->> TG_ARGV[1], '')::uuid;
    project uuid;
BEGIN
    -- Protection intents are frozen customer configuration. Only the original
    -- journal's operational progress may change during a capture hold.
    IF TG_TABLE_NAME = 'object_version_protection' AND TG_OP = 'UPDATE' THEN
        IF (before_row - ARRAY['state','lease_token','lease_until','retry_at','dispatched','last_error_code','updated_at','event_hold_baseline'])
            IS DISTINCT FROM (after_row - ARRAY['state','lease_token','lease_until','retry_at','dispatched','last_error_code','updated_at','event_hold_baseline'])
            OR ((before_row ->> 'dispatched')::boolean AND NOT (after_row ->> 'dispatched')::boolean)
            OR ((before_row ->> 'state') IN ('ready','failed') AND before_row IS DISTINCT FROM after_row)
            OR (nullif(before_row -> 'event_hold_baseline', 'null'::jsonb) IS NOT NULL AND nullif(before_row -> 'event_hold_baseline', 'null'::jsonb) IS DISTINCT FROM nullif(after_row -> 'event_hold_baseline', 'null'::jsonb))
            OR ((before_row ->> 'dispatched')::boolean AND nullif(before_row -> 'event_hold_baseline', 'null'::jsonb) IS DISTINCT FROM nullif(after_row -> 'event_hold_baseline', 'null'::jsonb))
            OR ((after_row ->> 'state') = 'failed' AND (after_row ->> 'last_error_code') NOT IN ('preparation_failed','provider_rejected'))
            OR ((after_row ->> 'state') = 'failed' AND (after_row ->> 'dispatched')::boolean AND (after_row ->> 'last_error_code') <> 'provider_rejected') THEN
            RAISE EXCEPTION 'original protection intent and settled evidence are immutable'
                USING ERRCODE = '23514', CONSTRAINT = 'object_protection_original_immutable';
        END IF;
        RETURN NEW;
    END IF;
    PERFORM generation FROM project_environment_clone_configuration_clock WHERE singleton FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'configuration synchronization clock is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    FOR project IN
        SELECT DISTINCT p.project_id FROM (
            SELECT before_id AS project_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT after_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT a.project_id FROM apps a
                WHERE TG_ARGV[0] = 'app' AND a.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM deployments d JOIN apps a ON a.id = d.app_id
                WHERE TG_ARGV[0] = 'deployment' AND d.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM object_buckets b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'bucket' AND b.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM managed_postgres_bindings b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'database' AND b.database_id IN (before_id, after_id)
        ) p WHERE p.project_id IS NOT NULL ORDER BY p.project_id
    LOOP
        PERFORM assert_clone_configuration_mutable(project);
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_bucket_write_fences) OR EXISTS(SELECT 1 FROM project_environment_clone_configuration_guards WHERE state='held') THEN
  RAISE EXCEPTION USING ERRCODE='23514',CONSTRAINT='object_protection_capture_admission_down_held',MESSAGE='Release capture holds before removing protection admission enforcement';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER object_protection_capture_admission ON object_version_protection;
DROP FUNCTION fence_object_protection_capture_admission();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_clone_configuration_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    before_row jsonb := CASE WHEN TG_OP <> 'INSERT' THEN to_jsonb(OLD) ELSE '{}'::jsonb END;
    after_row jsonb := CASE WHEN TG_OP <> 'DELETE' THEN to_jsonb(NEW) ELSE '{}'::jsonb END;
    before_id uuid := nullif(before_row ->> TG_ARGV[1], '')::uuid;
    after_id uuid := nullif(after_row ->> TG_ARGV[1], '')::uuid;
    project uuid;
BEGIN
    PERFORM generation FROM project_environment_clone_configuration_clock WHERE singleton FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'configuration synchronization clock is missing'
            USING ERRCODE = '55000', CONSTRAINT = 'clone_configuration_guard_missing';
    END IF;
    FOR project IN
        SELECT DISTINCT p.project_id FROM (
            SELECT before_id AS project_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT after_id WHERE TG_ARGV[0] = 'project'
            UNION ALL SELECT a.project_id FROM apps a
                WHERE TG_ARGV[0] = 'app' AND a.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM deployments d JOIN apps a ON a.id = d.app_id
                WHERE TG_ARGV[0] = 'deployment' AND d.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM object_buckets b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'bucket' AND b.id IN (before_id, after_id)
            UNION ALL SELECT a.project_id FROM managed_postgres_bindings b JOIN apps a ON a.id = b.app_id
                WHERE TG_ARGV[0] = 'database' AND b.database_id IN (before_id, after_id)
        ) p WHERE p.project_id IS NOT NULL ORDER BY p.project_id
    LOOP
        PERFORM assert_clone_configuration_mutable(project);
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
