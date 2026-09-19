-- +goose Up
-- +goose StatementBegin

-- Network-level CIDR policy is reusable by every attached workload. An empty
-- list preserves the legacy allow-all behavior; reconciliation still remains
-- fail-closed while the network or fabric is unavailable.
ALTER TABLE private_networks
  ADD COLUMN IF NOT EXISTS allowed_cidrs cidr[] NOT NULL DEFAULT '{}'::cidr[];

ALTER TABLE private_networks
  DROP CONSTRAINT IF EXISTS private_network_allowed_cidrs_count_chk;
ALTER TABLE private_networks
  ADD CONSTRAINT private_network_allowed_cidrs_count_chk
  CHECK (cardinality(allowed_cidrs) <= 64);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE private_networks
  DROP CONSTRAINT IF EXISTS private_network_allowed_cidrs_count_chk;
ALTER TABLE private_networks
  DROP COLUMN IF EXISTS allowed_cidrs;

-- +goose StatementEnd
