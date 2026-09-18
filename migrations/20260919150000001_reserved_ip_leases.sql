-- +goose Up
-- +goose StatementBegin

-- ADR-171: provider-neutral reserved public IP ownership. The row is a
-- control-plane lease; a connector must make the address reachable before it
-- advances pending -> assigned.
CREATE TABLE IF NOT EXISTS reserved_ip_leases (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  region        text NOT NULL,
  address       inet NOT NULL,
  status        text NOT NULL DEFAULT 'available',
  app_id        uuid REFERENCES apps(id) ON DELETE SET NULL,
  node_id       text,
  generation    bigint NOT NULL DEFAULT 0,
  status_detail text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT reserved_ip_leases_address_key UNIQUE (address),
  CONSTRAINT reserved_ip_leases_region_chk CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT reserved_ip_leases_family_chk CHECK (family(address) IN (4, 6)),
  CONSTRAINT reserved_ip_leases_status_chk CHECK (status IN ('available', 'pending', 'assigned', 'error')),
  CONSTRAINT reserved_ip_leases_generation_chk CHECK (generation >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS reserved_ip_leases_app_key
  ON reserved_ip_leases (app_id)
  WHERE app_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS reserved_ip_leases_account_region_idx
  ON reserved_ip_leases (account_id, region, status, updated_at DESC);

CREATE OR REPLACE FUNCTION reserved_ip_leases_set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS reserved_ip_leases_set_updated_at_trg ON reserved_ip_leases;
CREATE TRIGGER reserved_ip_leases_set_updated_at_trg
BEFORE UPDATE ON reserved_ip_leases
FOR EACH ROW EXECUTE FUNCTION reserved_ip_leases_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS reserved_ip_leases_set_updated_at_trg ON reserved_ip_leases;
DROP FUNCTION IF EXISTS reserved_ip_leases_set_updated_at();
DROP INDEX IF EXISTS reserved_ip_leases_account_region_idx;
DROP INDEX IF EXISTS reserved_ip_leases_app_key;
DROP TABLE IF EXISTS reserved_ip_leases;

-- +goose StatementEnd
