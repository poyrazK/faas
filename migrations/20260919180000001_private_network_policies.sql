-- +goose Up
-- +goose StatementBegin

-- Optional, provider-neutral private-network policy. Empty preserves the
-- original allow-all behavior for existing attachments; populated rows are
-- enforced by vmmd on both private egress and ingress.
ALTER TABLE app_private_network_attachments
  ADD COLUMN IF NOT EXISTS allowed_cidrs cidr[] NOT NULL DEFAULT '{}'::cidr[];

ALTER TABLE app_private_network_attachments
  DROP CONSTRAINT IF EXISTS app_private_network_attachment_allowed_cidr_count_chk;
ALTER TABLE app_private_network_attachments
  ADD CONSTRAINT app_private_network_attachment_allowed_cidr_count_chk
  CHECK (cardinality(allowed_cidrs) <= 64);

-- Re-emit the notification function so policy changes converge live through
-- the existing app_changed subscriber.
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

-- +goose Down
-- +goose StatementBegin

ALTER TABLE app_private_network_attachments
  DROP CONSTRAINT IF EXISTS app_private_network_attachment_allowed_cidr_count_chk;
ALTER TABLE app_private_network_attachments
  DROP COLUMN IF EXISTS allowed_cidrs;

-- +goose StatementEnd
