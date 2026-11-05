-- filename: 20261104001702109_workflow_event_bounded_retention.sql

-- migration-compat: contract
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- A run's events can outlive the run. Owners keep those live rows attached to
-- their app until a bounded background sweep moves them to the archive.
CREATE TABLE IF NOT EXISTS workflow_event_owners (
    run_id uuid PRIMARY KEY,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS workflow_event_owners_pending_idx ON workflow_event_owners (deleted_at, run_id)
    WHERE deleted_at IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_ensure_owner() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_app uuid;
BEGIN
    -- Also serialize a direct app-id change with this ownership check.
    SELECT app_id INTO owner_app FROM workflow_runs WHERE id = NEW.run_id FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workflow event run does not exist' USING ERRCODE = '23503';
    END IF;
    -- A writer may already hold the run while an app purge holds its parent.
    -- Never wait for that parent and create a lock cycle with the purge.
    PERFORM 1 FROM apps WHERE id = owner_app FOR KEY SHARE SKIP LOCKED;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workflow event app is being changed' USING ERRCODE = '40001';
    END IF;
    INSERT INTO workflow_event_owners (run_id, app_id) VALUES (NEW.run_id, owner_app)
    ON CONFLICT (run_id) DO UPDATE SET run_id = EXCLUDED.run_id
        WHERE workflow_event_owners.app_id = EXCLUDED.app_id AND workflow_event_owners.deleted_at IS NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'workflow event owner conflicts with retained events' USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS workflow_event_ensure_owner ON workflow_events;
CREATE TRIGGER workflow_event_ensure_owner BEFORE INSERT OR UPDATE OF run_id ON workflow_events
    FOR EACH ROW EXECUTE FUNCTION workflow_event_ensure_owner();

-- Existing rows acquire an owner when their run is deleted. The insertion
-- trigger enforces run ownership for new and changed rows. An owner removes
-- its events when its app is permanently deleted.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_event_owner_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM workflow_events WHERE run_id = OLD.run_id;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS workflow_event_owner_delete ON workflow_event_owners;
CREATE TRIGGER workflow_event_owner_delete BEFORE DELETE ON workflow_event_owners
    FOR EACH ROW EXECUTE FUNCTION workflow_event_owner_delete();

ALTER TABLE workflow_events DROP CONSTRAINT IF EXISTS workflow_events_run_id_fkey;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION workflow_run_retained_id_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.id IS NOT DISTINCT FROM OLD.id AND NEW.app_id IS NOT DISTINCT FROM OLD.app_id THEN
            RETURN NEW;
        END IF;
        -- Preserve the old foreign key's protection against changing the run
        -- id, and prevent moving retained evidence to another app.
        IF EXISTS (SELECT 1 FROM workflow_events WHERE run_id = OLD.id) THEN
            RAISE EXCEPTION 'workflow run has events' USING ERRCODE = '23503';
        END IF;
        DELETE FROM workflow_event_owners WHERE run_id = OLD.id;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        -- An insert can hold this ID lock while waiting for our run row.
        IF NOT pg_try_advisory_xact_lock(hashtextextended('workflow-event-owner/' || NEW.id::text, 0)) THEN
            RAISE EXCEPTION 'workflow run id is busy' USING ERRCODE = '40001';
        END IF;
    ELSE
        PERFORM pg_advisory_xact_lock(hashtextextended('workflow-event-owner/' || NEW.id::text, 0));
    END IF;
    IF EXISTS (SELECT 1 FROM workflow_event_owners WHERE run_id = NEW.id)
       OR EXISTS (SELECT 1 FROM workflow_event_archive WHERE run_id = NEW.id) THEN
        RAISE EXCEPTION 'workflow run id has retained events' USING ERRCODE = '23505';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS workflow_run_retained_id_guard ON workflow_runs;
CREATE TRIGGER workflow_run_retained_id_guard BEFORE INSERT OR UPDATE OF id, app_id ON workflow_runs
    FOR EACH ROW EXECUTE FUNCTION workflow_run_retained_id_guard();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION archive_workflow_events() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM apps WHERE id = OLD.app_id FOR KEY SHARE SKIP LOCKED;
    IF NOT FOUND THEN
        IF EXISTS (SELECT 1 FROM apps WHERE id = OLD.app_id) THEN
            RETURN NULL;
        END IF;
        -- The app itself is being deleted. Historical events without an owner
        -- predate this migration and must also be removed with the app.
        -- A competing insert can be waiting for this run row while holding
        -- its ID lock. Abort app deletion for retry rather than deadlocking.
        IF NOT pg_try_advisory_xact_lock(hashtextextended('workflow-event-owner/' || OLD.id::text, 0)) THEN
            RAISE EXCEPTION 'workflow run id is busy' USING ERRCODE = '40001';
        END IF;
        DELETE FROM workflow_events WHERE run_id = OLD.id;
        RETURN OLD;
    END IF;
    -- A concurrent insertion of this same run id must not wait on the run
    -- while we wait on its advisory lock. Skip and let retention retry.
    IF NOT pg_try_advisory_xact_lock(hashtextextended('workflow-event-owner/' || OLD.id::text, 0)) THEN
        RETURN NULL;
    END IF;
    IF EXISTS (SELECT 1 FROM workflow_events WHERE run_id = OLD.id) THEN
        INSERT INTO workflow_event_owners (run_id, app_id, deleted_at)
        VALUES (OLD.id, OLD.app_id, clock_timestamp())
        ON CONFLICT (run_id) DO UPDATE SET deleted_at = EXCLUDED.deleted_at
            WHERE workflow_event_owners.app_id = EXCLUDED.app_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'workflow event owner conflicts with run' USING ERRCODE = '23503';
        END IF;
    ELSE
        -- A run whose last event expired while it was still present has no
        -- retained work and must not leave a permanent owner behind.
        DELETE FROM workflow_event_owners WHERE run_id = OLD.id;
    END IF;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Keep the owner and its triggers across binary rollbacks. Restoring the old
-- cascade would delete retained live events or require an unbounded backfill.
SELECT 1;
