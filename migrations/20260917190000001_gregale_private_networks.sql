-- +goose Up
-- +goose StatementBegin

-- Gregale-owned private-network fabric. These rows are the control-plane
-- contract; host bridges and overlays consume them asynchronously.
CREATE TABLE IF NOT EXISTS private_networks (
  id            text PRIMARY KEY,
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name          text NOT NULL,
  region        text NOT NULL,
  cidr          cidr NOT NULL,
  status        text NOT NULL DEFAULT 'ready',
  status_detail text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT private_network_id_chk
    CHECK (id ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT private_network_account_name_chk
    CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT private_network_region_chk
    CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT private_network_status_chk
    CHECK (status IN ('ready', 'error')),
  CONSTRAINT private_network_cidr_shape_chk
    CHECK (
      family(cidr) = 4
      AND masklen(cidr) BETWEEN 16 AND 28
      AND (
        cidr <<= '10.0.0.0/8'::cidr
        OR cidr <<= '172.16.0.0/12'::cidr
        OR cidr <<= '192.168.0.0/16'::cidr
      )
    ),
  CONSTRAINT private_network_account_name_key UNIQUE (account_id, name)
);

CREATE INDEX IF NOT EXISTS private_networks_account_idx
  ON private_networks (account_id, name ASC, id ASC);

CREATE OR REPLACE FUNCTION private_network_set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS private_network_set_updated_at_trg ON private_networks;
CREATE TRIGGER private_network_set_updated_at_trg
BEFORE UPDATE ON private_networks
FOR EACH ROW EXECUTE FUNCTION private_network_set_updated_at();

-- A member receives one stable address per network. The gateway is reserved at
-- network+1 and the allocator starts at network+2; the final broadcast address
-- is never allocated.
CREATE TABLE IF NOT EXISTS private_network_addresses (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  network_id text NOT NULL REFERENCES private_networks(id) ON DELETE CASCADE,
  owner_type text NOT NULL,
  owner_id   text NOT NULL,
  address    inet NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT private_network_address_family_chk CHECK (family(address) = 4),
  CONSTRAINT private_network_address_owner_type_chk CHECK (length(owner_type) BETWEEN 1 AND 64),
  CONSTRAINT private_network_address_owner_id_chk CHECK (length(owner_id) BETWEEN 1 AND 128),
  CONSTRAINT private_network_address_network_ip_key UNIQUE (network_id, address),
  CONSTRAINT private_network_address_owner_key UNIQUE (network_id, owner_type, owner_id)
);

CREATE INDEX IF NOT EXISTS private_network_addresses_account_idx
  ON private_network_addresses (account_id, network_id, created_at ASC);

CREATE INDEX IF NOT EXISTS app_private_network_attachments_network_idx
  ON app_private_network_attachments (account_id, network_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS app_private_network_attachments_network_idx;
DROP TABLE IF EXISTS private_network_addresses;
DROP TRIGGER IF EXISTS private_network_set_updated_at_trg ON private_networks;
DROP FUNCTION IF EXISTS private_network_set_updated_at();
DROP TABLE IF EXISTS private_networks;

-- +goose StatementEnd
