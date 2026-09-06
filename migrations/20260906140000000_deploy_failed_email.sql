-- +goose Up
-- +goose StatementBegin

-- I1 / issue #1397: persist the deploy-failed email cooldown on the app
-- row. The timestamp is claimed with one atomic UPDATE, so multiple apid
-- replicas cannot send more than one failure email for an app in an hour.
ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS last_deploy_failed_email_at timestamptz;

-- A deployment can fail in imaged, builderd, vmmd, or an API-side enqueue
-- path. The trigger is the shared source of truth for all of those writers;
-- pg_notify is transactional and is delivered only after the status change
-- commits. The apid subscriber then loads the durable row and sends mail.
CREATE OR REPLACE FUNCTION deployments_failed_notify() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.status = 'failed'
       AND OLD.status IS DISTINCT FROM NEW.status THEN
        PERFORM pg_notify('deployment_changed', json_build_object(
            'kind', 'failed',
            'app_id', NEW.app_id,
            'deployment_id', NEW.id,
            'to', NEW.id,
            'status', NEW.status,
            'image_digest', NEW.image_digest
        )::text);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS deployments_failed_notify_trg ON deployments;
CREATE TRIGGER deployments_failed_notify_trg
AFTER UPDATE OF status ON deployments
FOR EACH ROW
EXECUTE FUNCTION deployments_failed_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployments_failed_notify_trg ON deployments;
DROP FUNCTION IF EXISTS deployments_failed_notify();
ALTER TABLE apps DROP COLUMN IF EXISTS last_deploy_failed_email_at;
-- +goose StatementEnd
