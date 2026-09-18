-- +goose Up
-- +goose StatementBegin

-- ADR-184: operator-owned public addresses. A claimed row is linked to one
-- account-scoped reserved_ip_leases row; the claim transaction creates both.
CREATE TABLE IF NOT EXISTS reserved_ip_inventory (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  region        text NOT NULL,
  address       inet NOT NULL,
  provider_ref  text NOT NULL DEFAULT '',
  status        text NOT NULL DEFAULT 'available',
  lease_id      uuid REFERENCES reserved_ip_leases(id) ON DELETE SET NULL,
  status_detail text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT reserved_ip_inventory_address_key UNIQUE (address),
  CONSTRAINT reserved_ip_inventory_region_chk CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT reserved_ip_inventory_family_chk CHECK (family(address) IN (4, 6)),
  CONSTRAINT reserved_ip_inventory_status_chk CHECK (status IN ('available', 'claimed', 'retired')),
  CONSTRAINT reserved_ip_inventory_claim_chk CHECK (
    (status = 'claimed' AND lease_id IS NOT NULL) OR
    (status <> 'claimed' AND lease_id IS NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS reserved_ip_inventory_provider_ref_key
  ON reserved_ip_inventory (provider_ref)
  WHERE provider_ref <> '';

CREATE UNIQUE INDEX IF NOT EXISTS reserved_ip_inventory_lease_key
  ON reserved_ip_inventory (lease_id)
  WHERE lease_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS reserved_ip_inventory_region_status_idx
  ON reserved_ip_inventory (region, status, updated_at DESC);

CREATE OR REPLACE FUNCTION reserved_ip_inventory_set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS reserved_ip_inventory_set_updated_at_trg ON reserved_ip_inventory;
CREATE TRIGGER reserved_ip_inventory_set_updated_at_trg
BEFORE UPDATE ON reserved_ip_inventory
FOR EACH ROW EXECUTE FUNCTION reserved_ip_inventory_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS reserved_ip_inventory_set_updated_at_trg ON reserved_ip_inventory;
DROP FUNCTION IF EXISTS reserved_ip_inventory_set_updated_at();
DROP INDEX IF EXISTS reserved_ip_inventory_region_status_idx;
DROP INDEX IF EXISTS reserved_ip_inventory_lease_key;
DROP INDEX IF EXISTS reserved_ip_inventory_provider_ref_key;
DROP TABLE IF EXISTS reserved_ip_inventory;

-- +goose StatementEnd
