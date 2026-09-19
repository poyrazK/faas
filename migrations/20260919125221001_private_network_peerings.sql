-- +goose Up
-- +goose StatementBegin

-- Durable, account-scoped peering intent. Network IDs are canonicalized by the
-- state layer (left_network_id < right_network_id), so this unique key also
-- blocks reverse-order duplicates.
CREATE TABLE IF NOT EXISTS private_network_peerings (
  id               text PRIMARY KEY,
  account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  left_network_id  text NOT NULL REFERENCES private_networks(id) ON DELETE RESTRICT,
  right_network_id text NOT NULL REFERENCES private_networks(id) ON DELETE RESTRICT,
  region           text NOT NULL,
  status           text NOT NULL DEFAULT 'pending',
  status_detail    text NOT NULL DEFAULT 'network route convergence is pending',
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT private_network_peering_id_chk
    CHECK (id ~ '^peer-[a-z0-9-]{1,58}$'),
  CONSTRAINT private_network_peering_region_chk
    CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
  CONSTRAINT private_network_peering_network_order_chk
    CHECK (left_network_id < right_network_id),
  CONSTRAINT private_network_peering_status_chk
    CHECK (status IN ('pending', 'ready', 'error')),
  CONSTRAINT private_network_peering_pair_key
    UNIQUE (account_id, left_network_id, right_network_id)
);

CREATE INDEX IF NOT EXISTS private_network_peerings_account_idx
  ON private_network_peerings (account_id, left_network_id, right_network_id, id);

DROP TRIGGER IF EXISTS private_network_peering_set_updated_at_trg ON private_network_peerings;
CREATE TRIGGER private_network_peering_set_updated_at_trg
BEFORE UPDATE ON private_network_peerings
FOR EACH ROW EXECUTE FUNCTION private_network_set_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TRIGGER IF EXISTS private_network_peering_set_updated_at_trg ON private_network_peerings;
DROP INDEX IF EXISTS private_network_peerings_account_idx;
DROP TABLE IF EXISTS private_network_peerings;

-- +goose StatementEnd
