-- filename: 20261011232006374_clone_checkpoint_unused_reservation_guard.sql

-- +goose Up
-- ADR-590: authenticate local unused target completion while preserving source holds.
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
    -- Only an original complete manifest can admit a private target INSERT.
    -- Existing rows, source configuration, credentials and policy mutations
    -- retain the project-wide guard. No caller-set session bypass exists.
    IF TG_TABLE_NAME = 'object_buckets' AND TG_OP = 'INSERT' THEN
        IF EXISTS (
            SELECT 1
            FROM project_environment_clone_operations o
            JOIN apps a ON a.id = NEW.app_id AND a.project_id = o.project_id AND a.account_id = o.account_id
            JOIN project_environment_clone_configuration_guards g ON g.project_id = o.project_id
            JOIN object_buckets source ON source.id = NEW.environment_clone_source_bucket_id
            JOIN object_bucket_write_fences f ON f.bucket_id = source.id AND f.clone_operation_id = o.id AND f.token = o.id
            JOIN project_environment_clone_object_manifests m ON m.operation_id = o.id AND m.source_bucket_id = source.id
            WHERE o.id = NEW.environment_clone_operation_id AND o.status = 'capturing' AND o.lease_until > clock_timestamp()
                AND g.state = 'held' AND g.operation_id = o.id AND g.account_id = o.account_id
                AND g.source_environment = o.source_environment AND g.source_revision_hash = o.source_revision_hash
                AND jsonb_array_length(o.resources) = 2
                AND o.resources->0->>'kind' = 'data_checkpoint' AND o.resources->0->>'status' = 'captured'
                AND o.resources->0->>'source_id' = o.id::text AND o.resources->0->>'name' = o.source_environment
                AND o.resources->1->>'kind' = 'data_checkpoint_receipt' AND o.resources->1->>'status' = 'captured'
                AND o.resources->1->>'source_id' = o.resources->0->>'target_id'
                AND o.resources->1->>'capture_point' = o.resources->0->>'capture_point'
                AND NEW.account_id = o.account_id AND NEW.scope = o.target_environment AND NEW.scope <> source.scope
                AND source.account_id = o.account_id AND source.app_id = NEW.app_id AND source.state = 'ready'
                AND f.backend_id = source.backend_id AND f.backend_fingerprint = source.backend_fingerprint AND f.physical_name = source.physical_name
                AND NEW.backend_id = source.backend_id AND NEW.backend_fingerprint = source.backend_fingerprint AND NEW.region = source.region
                AND NEW.state = 'provisioning' AND NOT NEW.public_read AND coalesce(NEW.serve_at, '') = ''
                AND NEW.physical_name = 'gregale-' || replace(NEW.id::text, '-', '') AND NEW.physical_name <> source.physical_name
                AND left(NEW.name, length('env-' || o.target_environment || '-')) = 'env-' || o.target_environment || '-'
                AND right(NEW.name, 12) ~ '^[a-f0-9]{12}$' AND length(NEW.name) = length('env-' || o.target_environment || '-') + 12
                AND m.target_bucket_id = NEW.id AND m.captured_at_exact = o.resources->0->>'capture_point'
                AND m.captured_at >= g.held_at
                AND m.object_count = (SELECT count(*) FROM project_environment_clone_object_entries e
                    WHERE e.operation_id = m.operation_id AND e.source_bucket_id = m.source_bucket_id)
            FOR SHARE OF o, g, f, source
        ) THEN RETURN NEW; END IF;
    END IF;
    -- Only journal-backed UNUSED reservation completion can change an
    -- existing target under its OWN source hold. No session bypass exists.
    IF TG_TABLE_NAME = 'object_buckets' AND TG_OP = 'UPDATE' THEN
        IF OLD.state = 'provisioning' AND NEW.state = 'deleted'
            AND OLD.lease_token IS NULL AND OLD.lease_until IS NULL
            AND NOT OLD.public_read AND OLD.serve_at IS NULL
            AND NEW.attempt_count = 0 AND NEW.last_error_code = ''
            AND (before_row - ARRAY['state','attempt_count','last_error_code','retry_at','updated_at'])
                = (after_row - ARRAY['state','attempt_count','last_error_code','retry_at','updated_at'])
            AND EXISTS (
                SELECT 1 FROM project_environment_clone_operations o
                JOIN apps a ON a.id = OLD.app_id AND a.project_id = o.project_id AND a.account_id = o.account_id
                JOIN project_environment_clone_configuration_guards g ON g.project_id = o.project_id
                JOIN project_environment_clone_checkpoint_unused_object_reservations u ON u.operation_id = o.id
                    AND u.source_bucket_id = OLD.environment_clone_source_bucket_id AND u.target_bucket_id = OLD.id
                JOIN project_environment_clone_object_manifests m ON m.operation_id = o.id
                    AND m.source_bucket_id = u.source_bucket_id AND m.target_bucket_id = OLD.id
                WHERE o.id = OLD.environment_clone_operation_id AND o.status = 'compensating'
                    AND o.lease_token IS NOT NULL AND o.lease_until > clock_timestamp()
                    AND g.state = 'held' AND g.operation_id = o.id AND g.account_id = o.account_id
                    AND g.source_environment = o.source_environment AND g.source_revision_hash = o.source_revision_hash
                    AND OLD.account_id = o.account_id AND OLD.scope = o.target_environment
                    AND OLD.id <> OLD.environment_clone_source_bucket_id
                    AND OLD.physical_name = 'gregale-' || replace(OLD.id::text, '-', '')
                    AND u.reservation->>'ID' = OLD.id::text AND u.reservation->>'AppID' = OLD.app_id::text
                    AND u.reservation->>'AccountID' = OLD.account_id::text AND u.reservation->>'Scope' = OLD.scope
                    AND u.reservation->>'PhysicalName' = OLD.physical_name
                    AND u.reservation->>'EnvironmentCloneOperationID' = o.id::text
                    AND u.reservation->>'EnvironmentCloneSourceBucketID' = u.source_bucket_id::text
                    AND o.resources->0->>'kind' = 'data_checkpoint' AND o.resources->0->>'status' = 'captured'
                    AND o.resources->(jsonb_array_length(o.resources)-1)->>'kind' = 'data_checkpoint_adoption'
                    AND NOT EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_targets t
                        WHERE t.operation_id = o.id AND t.source_bucket_id = u.source_bucket_id)
                    AND NOT EXISTS(SELECT 1 FROM object_storage_s3_credentials c WHERE c.bucket_id = OLD.id)
                    AND NOT EXISTS(SELECT 1 FROM object_storage_access_grants r WHERE r.bucket_id = OLD.id)
                FOR SHARE OF o, g, u, m
            ) THEN RETURN NEW; END IF;
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
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_unused_object_reservations u
 JOIN project_environment_clone_operations o ON o.id=u.operation_id
 JOIN project_environment_clone_configuration_guards g ON g.project_id=o.project_id
 WHERE o.status='compensating' AND g.state='held' AND g.operation_id=o.id) THEN
  RAISE EXCEPTION 'unused reservation completion still depends on owned source holds'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_unused_guard_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
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
    -- Only an original complete manifest can admit a private target INSERT.
    -- Existing rows, source configuration, credentials and policy mutations
    -- retain the project-wide guard. No caller-set session bypass exists.
    IF TG_TABLE_NAME = 'object_buckets' AND TG_OP = 'INSERT' THEN
        IF EXISTS (
            SELECT 1
            FROM project_environment_clone_operations o
            JOIN apps a ON a.id = NEW.app_id AND a.project_id = o.project_id AND a.account_id = o.account_id
            JOIN project_environment_clone_configuration_guards g ON g.project_id = o.project_id
            JOIN object_buckets source ON source.id = NEW.environment_clone_source_bucket_id
            JOIN object_bucket_write_fences f ON f.bucket_id = source.id AND f.clone_operation_id = o.id AND f.token = o.id
            JOIN project_environment_clone_object_manifests m ON m.operation_id = o.id AND m.source_bucket_id = source.id
            WHERE o.id = NEW.environment_clone_operation_id AND o.status = 'capturing' AND o.lease_until > clock_timestamp()
                AND g.state = 'held' AND g.operation_id = o.id AND g.account_id = o.account_id
                AND g.source_environment = o.source_environment AND g.source_revision_hash = o.source_revision_hash
                AND jsonb_array_length(o.resources) = 2
                AND o.resources->0->>'kind' = 'data_checkpoint' AND o.resources->0->>'status' = 'captured'
                AND o.resources->0->>'source_id' = o.id::text AND o.resources->0->>'name' = o.source_environment
                AND o.resources->1->>'kind' = 'data_checkpoint_receipt' AND o.resources->1->>'status' = 'captured'
                AND o.resources->1->>'source_id' = o.resources->0->>'target_id'
                AND o.resources->1->>'capture_point' = o.resources->0->>'capture_point'
                AND NEW.account_id = o.account_id AND NEW.scope = o.target_environment AND NEW.scope <> source.scope
                AND source.account_id = o.account_id AND source.app_id = NEW.app_id AND source.state = 'ready'
                AND f.backend_id = source.backend_id AND f.backend_fingerprint = source.backend_fingerprint AND f.physical_name = source.physical_name
                AND NEW.backend_id = source.backend_id AND NEW.backend_fingerprint = source.backend_fingerprint AND NEW.region = source.region
                AND NEW.state = 'provisioning' AND NOT NEW.public_read AND coalesce(NEW.serve_at, '') = ''
                AND NEW.physical_name = 'gregale-' || replace(NEW.id::text, '-', '') AND NEW.physical_name <> source.physical_name
                AND left(NEW.name, length('env-' || o.target_environment || '-')) = 'env-' || o.target_environment || '-'
                AND right(NEW.name, 12) ~ '^[a-f0-9]{12}$' AND length(NEW.name) = length('env-' || o.target_environment || '-') + 12
                AND m.target_bucket_id = NEW.id AND m.captured_at_exact = o.resources->0->>'capture_point'
                AND m.captured_at >= g.held_at
                AND m.object_count = (SELECT count(*) FROM project_environment_clone_object_entries e
                    WHERE e.operation_id = m.operation_id AND e.source_bucket_id = m.source_bucket_id)
            FOR SHARE OF o, g, f, source
        ) THEN RETURN NEW; END IF;
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
