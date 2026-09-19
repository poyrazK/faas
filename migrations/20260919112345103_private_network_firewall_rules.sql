-- +goose Up
-- +goose StatementBegin

-- Network-level protocol/port rules are provider-neutral and reusable by
-- every attached workload. An empty array preserves CIDR-only/allow-all
-- behavior; vmmd applies non-empty rules fail-closed.
ALTER TABLE private_networks
  ADD COLUMN IF NOT EXISTS firewall_rules jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE private_networks
  DROP CONSTRAINT IF EXISTS private_network_firewall_rules_shape_chk;
ALTER TABLE private_networks
  ADD CONSTRAINT private_network_firewall_rules_shape_chk
  CHECK (jsonb_typeof(firewall_rules) = 'array' AND jsonb_array_length(firewall_rules) <= 64);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE private_networks
  DROP CONSTRAINT IF EXISTS private_network_firewall_rules_shape_chk;
ALTER TABLE private_networks
  DROP COLUMN IF EXISTS firewall_rules;

-- +goose StatementEnd
