-- +goose Up
-- +goose StatementBegin

-- A private-network detach removes the attachment row, so the periodic
-- reconciler cannot discover it after the delete. Keep the existing advisory
-- app_changed signal for cache invalidation, but also record a durable
-- cleanup handoff in the notification outbox before the transaction commits.
-- The schedd LISTEN fast path handles the pg_notify envelope immediately;
-- RunNotificationOutbox replays the row after a LISTEN or schedd restart.
CREATE OR REPLACE FUNCTION app_private_network_attachment_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  detach_payload text;
  outbox_id bigint;
BEGIN
  IF TG_OP = 'DELETE' THEN
    detach_payload := json_build_object(
      'kind', 'private_network_attachment',
      'app_id', OLD.app_id::text,
      'account_id', OLD.account_id::text,
      'status', 'detached'
    )::text;

    INSERT INTO notification_outbox (channel, payload, available_at)
    VALUES ('private_network_attachment_changed', detach_payload, now() + interval '5 seconds')
    RETURNING id INTO outbox_id;

    PERFORM pg_notify(
      'private_network_attachment_changed',
      json_build_object(
        '_notification_outbox_id', outbox_id,
        'payload', detach_payload::json
      )::text
    );
    PERFORM pg_notify('app_changed', OLD.app_id::text);
    RETURN OLD;
  END IF;

  PERFORM pg_notify('app_changed', NEW.app_id::text);
  RETURN NEW;
END;
$$;

-- The trigger itself is already installed by the parent migration. Recreate
-- it here so upgrades that already ran that migration pick up the durable
-- function body without a manual operator step.
DROP TRIGGER IF EXISTS app_private_network_attachment_notify_trg
  ON app_private_network_attachments;
CREATE TRIGGER app_private_network_attachment_notify_trg
AFTER INSERT OR UPDATE OR DELETE ON app_private_network_attachments
FOR EACH ROW
EXECUTE FUNCTION app_private_network_attachment_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

CREATE OR REPLACE FUNCTION app_private_network_attachment_notify()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM pg_notify('app_changed', OLD.app_id::text);
    RETURN OLD;
  END IF;
  PERFORM pg_notify('app_changed', NEW.app_id::text);
  RETURN NEW;
END;
$$;

-- +goose StatementEnd
