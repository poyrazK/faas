-- +goose Up
-- +goose StatementBegin

-- The vmmd already detects the address it uses for cross-node control-plane
-- traffic. Persisting that address makes the same registration fact available
-- to schedd when it builds a regional private-network peer set.
ALTER TABLE compute_nodes
  ADD COLUMN IF NOT EXISTS overlay_ip inet;

ALTER TABLE compute_nodes
  DROP CONSTRAINT IF EXISTS compute_nodes_overlay_ip_family_chk;
ALTER TABLE compute_nodes
  ADD CONSTRAINT compute_nodes_overlay_ip_family_chk
  CHECK (overlay_ip IS NULL OR family(overlay_ip) = 4);

CREATE INDEX IF NOT EXISTS compute_nodes_active_region_overlay_idx
  ON compute_nodes (region, overlay_ip)
  WHERE active = true AND overlay_ip IS NOT NULL;

COMMENT ON COLUMN compute_nodes.overlay_ip IS
  'vmmd-registered IPv4 address on the operator-managed regional overlay';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS compute_nodes_active_region_overlay_idx;
ALTER TABLE compute_nodes
  DROP CONSTRAINT IF EXISTS compute_nodes_overlay_ip_family_chk;
ALTER TABLE compute_nodes
  DROP COLUMN IF EXISTS overlay_ip;

-- +goose StatementEnd
