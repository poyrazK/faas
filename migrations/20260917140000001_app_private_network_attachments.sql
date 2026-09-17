-- +goose Up
-- +goose StatementBegin

-- Provider-neutral private-network attachment intent. The row records the
-- customer request and its CIDR contract; a connector is responsible for
-- moving it from pending to ready in a later runtime slice. Keeping the
-- attachment separate from apps avoids widening the hot app projection while
-- the connector contract is still being validated.
CREATE TABLE IF NOT EXISTS app_private_network_attachments (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  app_id      uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  network_id  text NOT NULL,
  region      text NOT NULL,
  cidrs       cidr[] NOT NULL DEFAULT '{}'::cidr[],
  status      text NOT NULL DEFAULT 'pending',
  status_detail text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT app_private_network_attachment_network_id_chk
    CHECK (network_id ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT app_private_network_attachment_region_chk
    CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT app_private_network_attachment_status_chk
    CHECK (status IN ('pending', 'ready', 'error')),
  CONSTRAINT app_private_network_attachment_cidr_count_chk
    CHECK (cardinality(cidrs) <= 64),
  CONSTRAINT app_private_network_attachment_app_key UNIQUE (app_id)
);

CREATE INDEX IF NOT EXISTS app_private_network_attachments_account_idx
  ON app_private_network_attachments (account_id, updated_at DESC);

CREATE OR REPLACE FUNCTION app_private_network_attachment_set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS app_private_network_attachment_set_updated_at_trg
  ON app_private_network_attachments;
CREATE TRIGGER app_private_network_attachment_set_updated_at_trg
BEFORE UPDATE ON app_private_network_attachments
FOR EACH ROW
EXECUTE FUNCTION app_private_network_attachment_set_updated_at();

-- Keep the existing app_changed subscribers ready for the connector slice.
-- The current network attachment API is fail-closed while status=pending, but
-- changing intent still invalidates any future per-app network cache.
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

DROP TRIGGER IF EXISTS app_private_network_attachment_notify_trg
  ON app_private_network_attachments;
CREATE TRIGGER app_private_network_attachment_notify_trg
AFTER INSERT OR UPDATE OR DELETE ON app_private_network_attachments
FOR EACH ROW
EXECUTE FUNCTION app_private_network_attachment_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS app_private_network_attachment_notify_trg
  ON app_private_network_attachments;
DROP TRIGGER IF EXISTS app_private_network_attachment_set_updated_at_trg
  ON app_private_network_attachments;
DROP FUNCTION IF EXISTS app_private_network_attachment_notify();
DROP FUNCTION IF EXISTS app_private_network_attachment_set_updated_at();
DROP TABLE IF EXISTS app_private_network_attachments;

-- +goose StatementEnd
