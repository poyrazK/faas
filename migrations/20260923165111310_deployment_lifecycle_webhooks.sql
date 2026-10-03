-- +goose Up
-- +goose StatementBegin
-- Fan out deployment terminal transitions inside the same transaction that
-- changes deployment status. A process crash after commit cannot lose the
-- webhook, and a repeated write of the same status cannot enqueue it twice.
-- The existing delivery dispatcher owns signing, retries, and dead letters.
CREATE OR REPLACE FUNCTION enqueue_deployment_lifecycle_webhooks()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    event_name text;
    event_payload jsonb;
BEGIN
    IF NEW.status = OLD.status OR NEW.status NOT IN ('live', 'failed') THEN
        RETURN NEW;
    END IF;

    event_name := 'deployment.' || NEW.status;
    event_payload := jsonb_build_object(
        'app_id', NEW.app_id,
        'deployment_id', NEW.id,
        'status', NEW.status
    );
    IF NEW.status = 'failed' THEN
        event_payload := event_payload || jsonb_strip_nulls(jsonb_build_object(
            'error_code', NEW.error_code,
            'error_hint', NEW.error_hint,
            'error_why', NEW.error_why,
            'error_fix', NEW.error_fix
        ));
    END IF;

    INSERT INTO app_webhook_deliveries
        (webhook_id, app_id, account_id, event, payload)
    SELECT h.id, NEW.app_id, h.account_id, event_name, event_payload
      FROM app_webhooks h
      JOIN apps a ON a.id = NEW.app_id AND a.account_id = h.account_id
     WHERE h.app_id = NEW.app_id
       AND h.enabled
       AND (cardinality(h.event_filter) = 0 OR event_name = ANY(h.event_filter));
    RETURN NEW;
END;
$$;

-- A database may have this trigger even if its goose ledger is behind.
-- Replacing it in the migration transaction makes replay safe.
DROP TRIGGER IF EXISTS deployments_lifecycle_webhooks ON deployments;
CREATE TRIGGER deployments_lifecycle_webhooks
AFTER UPDATE OF status ON deployments
FOR EACH ROW EXECUTE FUNCTION enqueue_deployment_lifecycle_webhooks();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_lifecycle_webhooks ON deployments;
DROP FUNCTION IF EXISTS enqueue_deployment_lifecycle_webhooks();
-- +goose StatementEnd
