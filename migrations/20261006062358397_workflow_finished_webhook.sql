-- +goose Up
ALTER TABLE app_webhook_event_outbox
    DROP CONSTRAINT IF EXISTS app_webhook_event_outbox_event_chk;
ALTER TABLE app_webhook_event_outbox
    ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK (event IN (
        'usage_statement.finalized', 'app.parked', 'app.woken',
        'issue.created', 'issue.assigned', 'issue.resolved', 'issue.reopened',
        'issue.ignored', 'issue.regressed', 'issue.impact_threshold_reached',
        'routes.requirements.violated', 'routes.requirements.recovered',
        'routes.requirements.changed', 'routes.health.blocked',
        'routes.health.resumed', 'routes.health.aborted',
        'routes.monitor.violated', 'routes.monitor.recovered',
        'workflow.finished'
    ));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_capture_workflow_finished_webhook_event()
RETURNS trigger AS $$
DECLARE
    event_account_id uuid;
    recipient_ids uuid[];
    event_finished_at timestamptz;
BEGIN
    IF OLD.status IS NOT DISTINCT FROM NEW.status
       OR OLD.status IN ('succeeded', 'failed', 'dead')
       OR NEW.status NOT IN ('succeeded', 'failed', 'dead') THEN
        RETURN NEW;
    END IF;

    SELECT a.account_id,
           COALESCE(
               array_agg(h.id ORDER BY h.id) FILTER (WHERE h.id IS NOT NULL),
               ARRAY[]::uuid[]
           )
      INTO event_account_id, recipient_ids
      FROM apps a
      LEFT JOIN app_webhooks h
        ON h.app_id = NEW.app_id
       AND h.account_id = a.account_id
       AND h.scope = 'app'
       AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR 'workflow.finished' = ANY(h.event_filter))
     WHERE a.id = NEW.app_id
     GROUP BY a.account_id;

    IF COALESCE(cardinality(recipient_ids), 0) = 0 THEN
        RETURN NEW;
    END IF;

    event_finished_at := COALESCE(NEW.finished_at, clock_timestamp());
    INSERT INTO app_webhook_event_outbox (
        account_id, app_id, event, source_id, payload, recipient_webhook_ids
    ) VALUES (
        event_account_id,
        NEW.app_id,
        'workflow.finished',
        gen_random_uuid(),
        jsonb_build_object(
            'app_id', NEW.app_id::text,
            'run_id', NEW.id::text,
            'workflow_name', NEW.workflow_name,
            'status', NEW.status,
            'finished_at', event_finished_at,
            'resume_count', NEW.resume_count
        ),
        recipient_ids
    ) ON CONFLICT (event, source_id) DO NOTHING;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS workflow_runs_capture_finished_webhook_event ON workflow_runs;
CREATE TRIGGER workflow_runs_capture_finished_webhook_event
    AFTER UPDATE OF status ON workflow_runs
    FOR EACH ROW EXECUTE FUNCTION faas_capture_workflow_finished_webhook_event();

-- +goose Down
-- Keep the event constraint and trigger so committed workflow outcomes are
-- not discarded during a binary rollback.
SELECT 1;
