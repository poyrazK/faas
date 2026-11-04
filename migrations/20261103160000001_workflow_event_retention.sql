-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Keep event evidence after its run is pruned; permanent app deletion still
-- removes the archive. Existing writers continue to use workflow_events.
CREATE TABLE IF NOT EXISTS workflow_event_archive (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    event_name text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS workflow_event_archive_retention_idx ON workflow_event_archive (received_at, id);
CREATE INDEX IF NOT EXISTS workflow_event_archive_run_idx ON workflow_event_archive (run_id, event_name, received_at, id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION archive_workflow_events() RETURNS trigger AS $$
BEGIN
    -- Never wait for the parent while holding a run lock: an app purge can
    -- already hold that parent and be waiting for this run's cascade.
    PERFORM 1 FROM apps WHERE id = OLD.app_id FOR KEY SHARE SKIP LOCKED;
    IF NOT FOUND THEN
        IF EXISTS (SELECT 1 FROM apps WHERE id = OLD.app_id) THEN
            RETURN NULL; -- A concurrent app mutation owns the parent; retry later.
        END IF;
        RETURN OLD; -- The parent is being deleted by this transaction's cascade.
    END IF;
    INSERT INTO workflow_event_archive (id, run_id, app_id, event_name, payload, received_at)
    SELECT id, run_id, OLD.app_id, event_name, payload, received_at
    FROM workflow_events WHERE run_id = OLD.id
    ON CONFLICT (id) DO NOTHING;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS workflow_runs_archive_events ON workflow_runs;
CREATE TRIGGER workflow_runs_archive_events BEFORE DELETE ON workflow_runs
    FOR EACH ROW EXECUTE FUNCTION archive_workflow_events();

-- +goose Down
-- Preserve the archive and trigger across binary rollbacks.
SELECT 1;
